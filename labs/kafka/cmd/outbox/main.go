// Outbox pattern lab driver.
//
// mode=dualwrite  the anti-pattern: commit the order, then publish the event
//                 directly to Kafka. A crash between the two loses the event.
// mode=outbox     the pattern: insert the order and its event into an outbox
//                 table in one transaction. Debezium tails the WAL and
//                 publishes to Kafka; there is no gap to crash inside.
// mode=audit      compare orders in Postgres against events in Kafka
//                 (-source=direct for the dual-write topic, -source=cdc for
//                 the Debezium topic) and report missing events.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

const directTopic = "order-events"
const cdcTopic = "shop.public.outbox"

func main() {
	mode := flag.String("mode", "outbox", "dualwrite|outbox|audit")
	count := flag.Int("count", 1000, "orders to create")
	crashAt := flag.Int("crash-at", 0, "crash after committing this order, before publishing (0 = never)")
	source := flag.String("source", "cdc", "audit source: direct|cdc")
	flag.Parse()

	brokers := strings.Split(env("KAFKA_BROKERS", "kafka:9092"), ",")
	ctx := context.Background()
	pool := connect(ctx)
	defer pool.Close()

	switch *mode {
	case "dualwrite":
		dualwrite(ctx, pool, brokers, *count, *crashAt)
	case "outbox":
		outbox(ctx, pool, *count, *crashAt)
	case "audit":
		audit(ctx, pool, brokers, *source)
	default:
		fatalf("unknown mode %q", *mode)
	}
}

func dualwrite(ctx context.Context, pool *pgxpool.Pool, brokers []string, count, crashAt int) {
	w := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  directTopic,
		RequiredAcks:           kafka.RequireAll,
		BatchTimeout:           10 * time.Millisecond,
		AllowAutoTopicCreation: true,
	}
	defer w.Close()

	fmt.Printf("dualwrite: %d orders, commit first, publish second (crash-at=%d)\n", count, crashAt)
	inserted, published, skipped := 0, 0, 0
	for i := 0; i < count; i++ {
		ref := fmt.Sprintf("ord-%d", i)
		tag, err := pool.Exec(ctx,
			`INSERT INTO orders (order_ref, amount_cents) VALUES ($1, $2)
			 ON CONFLICT (order_ref) DO NOTHING`, ref, 100+i)
		if err != nil {
			fatalf("insert %s: %v", ref, err)
		}
		if tag.RowsAffected() == 0 {
			skipped++ // already created by a previous (crashed) run: no publish either
			continue
		}
		inserted++

		if crashAt > 0 && i == crashAt {
			// The order is committed. The event was never sent. This is the
			// dual-write gap: no transaction spans Postgres and Kafka.
			fmt.Printf("CRASH injected after committing %s, before publishing its event\n", ref)
			os.Exit(1)
		}

		if err := w.WriteMessages(ctx, kafka.Message{Key: []byte(ref), Value: []byte(ref)}); err != nil {
			fatalf("publish %s: %v", ref, err)
		}
		published++
	}
	fmt.Printf("done: inserted=%d published=%d skipped-existing=%d\n", inserted, published, skipped)
	fmt.Println("run 'make audit-direct' to compare the database against the topic")
}

func outbox(ctx context.Context, pool *pgxpool.Pool, count, crashAt int) {
	fmt.Printf("outbox: %d orders, order + event in ONE transaction (crash-at=%d)\n", count, crashAt)
	inserted, skipped := 0, 0
	for i := 0; i < count; i++ {
		ref := fmt.Sprintf("ord-%d", i)
		tx, err := pool.Begin(ctx)
		if err != nil {
			fatalf("begin: %v", err)
		}
		var id int64
		err = tx.QueryRow(ctx,
			`INSERT INTO orders (order_ref, amount_cents) VALUES ($1, $2)
			 ON CONFLICT (order_ref) DO NOTHING RETURNING id`, ref, 100+i).Scan(&id)
		if err != nil {
			tx.Rollback(ctx)
			skipped++ // order exists from a previous run; its outbox row committed with it
			continue
		}
		payload, _ := json.Marshal(map[string]any{"order_ref": ref, "amount_cents": 100 + i})
		if _, err := tx.Exec(ctx,
			`INSERT INTO outbox (aggregate_type, aggregate_id, event_type, payload)
			 VALUES ('order', $1, 'order.created', $2)`, ref, payload); err != nil {
			fatalf("outbox insert %s: %v", ref, err)
		}
		if err := tx.Commit(ctx); err != nil {
			fatalf("commit %s: %v", ref, err)
		}
		inserted++

		if crashAt > 0 && i == crashAt {
			// Crash in the same spot as dualwrite. Nothing diverges: the order
			// and its event committed atomically; Debezium picks it up later.
			fmt.Printf("CRASH injected after committing %s\n", ref)
			os.Exit(1)
		}
	}
	fmt.Printf("done: inserted=%d skipped-existing=%d\n", inserted, skipped)
	fmt.Println("give Debezium a few seconds, then run 'make audit-cdc'")
}

func audit(ctx context.Context, pool *pgxpool.Pool, brokers []string, source string) {
	rows, err := pool.Query(ctx, `SELECT order_ref FROM orders ORDER BY id`)
	if err != nil {
		fatalf("query orders: %v", err)
	}
	inDB := map[string]bool{}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			fatalf("scan: %v", err)
		}
		inDB[ref] = true
	}
	rows.Close()

	topic := directTopic
	if source == "cdc" {
		topic = cdcTopic
	}
	inKafka := scanTopic(ctx, brokers, topic, source)

	missing := []string{}
	for ref := range inDB {
		if !inKafka[ref] {
			missing = append(missing, ref)
		}
	}
	fmt.Printf("audit(%s): orders in postgres=%d, events in %s=%d\n", source, len(inDB), topic, len(inKafka))
	fmt.Printf("  orders with no event: %d", len(missing))
	if len(missing) > 0 && len(missing) <= 20 {
		fmt.Printf(" %v", missing)
	}
	fmt.Println()
	if len(missing) > 0 {
		fmt.Println("RESULT: committed state and published events diverged. exit 1")
		os.Exit(1)
	}
	fmt.Println("RESULT: every committed order has an event. exit 0")
}

// scanTopic reads every partition from the beginning and extracts order refs.
// Direct messages carry the ref as the value; Debezium messages carry JSON
// with the row under "after" (value converter schemas are disabled).
func scanTopic(ctx context.Context, brokers []string, topic, source string) map[string]bool {
	refs := map[string]bool{}
	conn, err := kafka.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		fatalf("dial: %v", err)
	}
	parts, err := conn.ReadPartitions(topic)
	conn.Close()
	if err != nil {
		fmt.Printf("  (topic %s not readable yet: %v)\n", topic, err)
		return refs
	}
	for _, p := range parts {
		leader := net.JoinHostPort(p.Leader.Host, strconv.Itoa(p.Leader.Port))
		c, err := kafka.DialLeader(ctx, "tcp", leader, topic, p.ID)
		if err != nil {
			fatalf("dial leader: %v", err)
		}
		first, last, err := c.ReadOffsets()
		if err != nil {
			fatalf("offsets: %v", err)
		}
		for read := first; read < last; {
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			batch := c.ReadBatch(1, 1<<20)
			for {
				msg, err := batch.ReadMessage()
				if err != nil {
					break
				}
				read = msg.Offset + 1
				if source == "direct" {
					refs[string(msg.Value)] = true
					continue
				}
				var change struct {
					After struct {
						AggregateID string `json:"aggregate_id"`
					} `json:"after"`
				}
				if err := json.Unmarshal(msg.Value, &change); err == nil && change.After.AggregateID != "" {
					refs[change.After.AggregateID] = true
				}
			}
			batch.Close()
		}
		c.Close()
	}
	return refs
}

func connect(ctx context.Context) *pgxpool.Pool {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatalf("DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatalf("connect: %v", err)
	}
	return pool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fatal: "+format+"\n", args...)
	os.Exit(1)
}
