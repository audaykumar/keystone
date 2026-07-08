// Circuit-breaker lab: a real HTTP chain load -> api -> {backend-a, backend-b}.
//
// One binary, three roles (-role via ROLE env). backend-a and backend-b are the
// SAME rate-based capacity model copied from labs/reliability/retry-storm/src
// (a healthy instance is simply never degraded). api calls both downstreams and
// applies, per downstream, a circuit breaker + bulkhead + load-shedding policy
// controlled entirely by environment variables so the same image runs the
// "break" and "test" scenarios with only compose overrides changing:
//
//	BREAKER=on|off        consult/update the per-downstream breaker state machine
//	BULKHEAD=on|off        on: one bounded pool per downstream. off: one shared
//	                        pool sized the same as the two split pools combined
//	SHED=on|off             on: a full breaker/pool fails fast (no queueing).
//	                        off: a blocked slot is waited for (open-loop callers
//	                        pile up instead of being told no)
//	BREAKER_THRESHOLD       consecutive downstream failures before the breaker trips
//	BREAKER_COOLDOWN_MS     how long an open breaker stays open before one probe
//	BULKHEAD_SIZE           total concurrent-call budget across both downstreams
//	DOWNSTREAM_A/B          base URLs for the two backends
//	ATTEMPT_TIMEOUT_MS      per-call timeout budget
package main

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	role := getenv("ROLE", "backend")
	switch role {
	case "backend":
		runBackend()
	case "api":
		runAPI()
	case "load":
		runLoad()
	default:
		fmt.Fprintf(os.Stderr, "unknown ROLE %q\n", role)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// backend: the dependency, either healthy or degraded. Copied unchanged from
// labs/reliability/retry-storm/src/main.go's rate-based capacity model.
// ---------------------------------------------------------------------------

func runBackend() {
	var received atomic.Int64
	baseLatency := time.Duration(getenvInt("LATENCY_MS", 5)) * time.Millisecond
	capacity := float64(getenvInt("CAPACITY", 220))
	degradedCapacity := float64(getenvInt("DEGRADED_CAPACITY", 90))
	degradedBaseFail := getenvFloat("DEGRADED_FAIL", 0.4)
	degraded := &atomic.Bool{}

	const nb = 10
	var buckets [nb]atomic.Int64
	var idx atomic.Int64
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			next := (idx.Load() + 1) % nb
			buckets[next].Store(0)
			idx.Store(next)
		}
	}()
	rate := func() float64 {
		var s int64
		for i := 0; i < nb; i++ {
			s += buckets[i].Load()
		}
		return float64(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		buckets[idx.Load()].Add(1)

		cap := capacity
		baseFail := 0.0
		if degraded.Load() {
			cap = degradedCapacity
			baseFail = degradedBaseFail
		}
		lat := baseLatency
		fail := baseFail
		if over := rate() - cap; over > 0 {
			lat += time.Duration(over/cap*1500) * time.Millisecond
			fail += over / cap
		}
		if fail > 0.95 {
			fail = 0.95
		}
		select {
		case <-time.After(lat):
		case <-r.Context().Done():
			return
		}
		if rand.Float64() < fail {
			http.Error(w, "backend overloaded", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("/degrade", func(w http.ResponseWriter, r *http.Request) {
		degraded.Store(r.URL.Query().Get("on") == "true")
		fmt.Fprintf(w, "degraded=%v\n", degraded.Load())
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%d", received.Load())
	})
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		received.Store(0)
		degraded.Store(false)
	})
	serve(mux)
}

// ---------------------------------------------------------------------------
// api: breaker + bulkhead + shedding in front of each downstream.
// ---------------------------------------------------------------------------

const (
	closed = iota
	open
	halfOpen
)

// breaker is a small closed -> open -> half-open state machine. allow()
// decides whether a call may proceed and whether it is the single half-open
// probe; recordResult() feeds the outcome back in.
type breaker struct {
	state     atomic.Int32 // closed | open | halfOpen
	failCount atomic.Int32
	openedAt  atomic.Int64 // UnixNano
	trips     atomic.Int64 // count of closed -> open transitions
	threshold int32
	cooldown  time.Duration
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{threshold: int32(threshold), cooldown: cooldown}
}

// allow reports whether a call may proceed, and whether it is the single
// probe attempt let through while the breaker is half-open.
func (b *breaker) allow() (ok bool, isProbe bool) {
	switch b.state.Load() {
	case closed:
		return true, false
	case open:
		openedAt := time.Unix(0, b.openedAt.Load())
		if time.Since(openedAt) >= b.cooldown && b.state.CompareAndSwap(open, halfOpen) {
			// This goroutine won the race to probe; everyone else sees
			// halfOpen and is shed below until the probe resolves.
			return true, true
		}
		return false, false
	case halfOpen:
		return false, false
	}
	return true, false
}

func (b *breaker) recordResult(success bool, wasProbe bool) {
	if success {
		if wasProbe || b.state.Load() == halfOpen {
			b.state.Store(closed)
		}
		b.failCount.Store(0)
		return
	}
	if wasProbe {
		b.openedAt.Store(time.Now().UnixNano())
		b.state.Store(open)
		return
	}
	if b.state.Load() == closed {
		n := b.failCount.Add(1)
		if n >= b.threshold && b.state.CompareAndSwap(closed, open) {
			b.openedAt.Store(time.Now().UnixNano())
			b.trips.Add(1)
		}
	}
}

func (b *breaker) name() string {
	switch b.state.Load() {
	case open:
		return "open"
	case halfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// downstream bundles one dependency's breaker, bulkhead pool, and counters.
type downstream struct {
	name         string
	url          string
	br           *breaker
	pool         chan struct{} // capacity token pool; shared across downstreams if BULKHEAD=off
	requests     atomic.Int64
	ok           atomic.Int64
	failCalls    atomic.Int64
	shedBreaker  atomic.Int64
	shedBulkhead atomic.Int64
}

type api struct {
	breakerOn  bool
	bulkheadOn bool
	shedOn     bool
	timeout    time.Duration
	client     *http.Client
	a, b       *downstream
}

func runAPI() {
	threshold := getenvInt("BREAKER_THRESHOLD", 5)
	cooldown := time.Duration(getenvInt("BREAKER_COOLDOWN_MS", 2000)) * time.Millisecond
	bulkheadSize := getenvInt("BULKHEAD_SIZE", 20)
	timeout := time.Duration(getenvInt("ATTEMPT_TIMEOUT_MS", 800)) * time.Millisecond

	a := &downstream{name: "A", url: getenv("DOWNSTREAM_A", "http://backend-a:8080"), br: newBreaker(threshold, cooldown)}
	b := &downstream{name: "B", url: getenv("DOWNSTREAM_B", "http://backend-b:8080"), br: newBreaker(threshold, cooldown)}

	bulkheadOn := getenv("BULKHEAD", "on") == "on"
	if bulkheadOn {
		// Same total budget as the shared-pool case, split so one
		// dependency cannot spend the other's share.
		each := bulkheadSize / 2
		if each < 1 {
			each = 1
		}
		a.pool = make(chan struct{}, each)
		b.pool = make(chan struct{}, each)
	} else {
		shared := make(chan struct{}, bulkheadSize)
		a.pool = shared
		b.pool = shared
	}

	s := &api{
		breakerOn:  getenv("BREAKER", "on") == "on",
		bulkheadOn: bulkheadOn,
		shedOn:     getenv("SHED", "on") == "on",
		timeout:    timeout,
		client:     &http.Client{},
		a:          a,
		b:          b,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/a", s.handler(a))
	mux.HandleFunc("/b", s.handler(b))
	mux.HandleFunc("/stats", s.statsHandler)
	mux.HandleFunc("/reset", s.resetHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	serve(mux)
}

func (s *api) handler(d *downstream) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.requests.Add(1)
		status := s.call(d)
		if status == http.StatusOK {
			d.ok.Add(1)
			io.WriteString(w, "ok")
			return
		}
		http.Error(w, "downstream unavailable", status)
	}
}

// call applies the breaker check, then the bulkhead acquire, then the
// downstream HTTP call, feeding the outcome back into the breaker.
func (s *api) call(d *downstream) int {
	isProbe := false
	if s.breakerOn {
		allowed, probe := d.br.allow()
		if !allowed {
			d.shedBreaker.Add(1)
			return http.StatusServiceUnavailable
		}
		isProbe = probe
	}

	if s.shedOn {
		select {
		case d.pool <- struct{}{}:
		default:
			d.shedBulkhead.Add(1)
			return http.StatusServiceUnavailable
		}
	} else {
		d.pool <- struct{}{}
	}
	defer func() { <-d.pool }()

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.url+"/work", nil)
	resp, err := s.client.Do(req)
	success := err == nil && resp != nil && resp.StatusCode == http.StatusOK
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if !success {
		d.failCalls.Add(1)
	}
	if s.breakerOn {
		d.br.recordResult(success, isProbe)
	}
	if success {
		return http.StatusOK
	}
	return http.StatusServiceUnavailable
}

func (s *api) statsHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "policy breaker=%v bulkhead=%v shed=%v\n", s.breakerOn, s.bulkheadOn, s.shedOn)
	for _, d := range []*downstream{s.a, s.b} {
		total := d.requests.Load()
		ok := d.ok.Load()
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(ok) / float64(total)
		}
		fmt.Fprintf(w, "%s requests=%d ok=%d ok_pct=%.1f fail_calls=%d shed_breaker=%d shed_bulkhead=%d breaker_state=%s breaker_trips=%d\n",
			d.name, total, ok, pct, d.failCalls.Load(), d.shedBreaker.Load(), d.shedBulkhead.Load(), d.br.name(), d.br.trips.Load())
	}
}

func (s *api) resetHandler(w http.ResponseWriter, r *http.Request) {
	for _, d := range []*downstream{s.a, s.b} {
		d.requests.Store(0)
		d.ok.Store(0)
		d.failCalls.Store(0)
		d.shedBreaker.Store(0)
		d.shedBulkhead.Store(0)
		d.br.state.Store(closed)
		d.br.failCount.Store(0)
		d.br.trips.Store(0)
	}
	io.WriteString(w, "reset\n")
}

// ---------------------------------------------------------------------------
// load: two independent open-loop generators, one per downstream-affecting
// endpoint, driving api concurrently and reporting per-backend outcomes.
// ---------------------------------------------------------------------------

type result struct {
	ok  bool
	lat time.Duration
}

func runLoad() {
	target := getenv("TARGET", "http://api:8080")
	backendB := getenv("BACKEND_B", "http://backend-b:8080")
	rateA := getenvInt("RATE_A", 60)
	rateB := getenvInt("RATE_B", 60)
	duration := time.Duration(getenvInt("DURATION_S", 20)) * time.Second
	label := getenv("LABEL", "run")

	client := &http.Client{Timeout: 3 * time.Second}
	http.Get(target + "/reset")
	http.Get(backendB + "/reset")
	http.Get(backendB + "/degrade?on=true")
	fmt.Printf("[%s] backend-b degraded; offering A=%d req/s B=%d req/s for %s\n", label, rateA, rateB, duration)

	var wg sync.WaitGroup
	resA := driveOpenLoop(&wg, client, target+"/a", rateA, duration)
	resB := driveOpenLoop(&wg, client, target+"/b", rateB, duration)
	wg.Wait()

	stats := fetchText(client, target+"/stats")
	http.Get(backendB + "/degrade?on=false")

	fmt.Printf("\n=== %s ===\n", label)
	report("A (healthy)", resA)
	report("B (degraded)", resB)
	fmt.Printf("\napi /stats:\n%s\n", stats)
}

// driveOpenLoop fires GETs at a fixed rate regardless of response time (the
// honest model for user traffic: closed-loop workers self-throttle and hide
// the cascade the bulkhead/breaker exists to prevent).
func driveOpenLoop(outer *sync.WaitGroup, client *http.Client, url string, rate int, duration time.Duration) *[]result {
	results := &[]result{}
	var mu sync.Mutex
	guard := make(chan struct{}, 4000)

	outer.Add(1)
	go func() {
		defer outer.Done()
		var wg sync.WaitGroup
		ticker := time.NewTicker(time.Second / time.Duration(rate))
		defer ticker.Stop()
		deadline := time.Now().Add(duration)
		for time.Now().Before(deadline) {
			<-ticker.C
			select {
			case guard <- struct{}{}:
			default:
				mu.Lock()
				*results = append(*results, result{ok: false, lat: 0})
				mu.Unlock()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-guard }()
				t := time.Now()
				resp, err := client.Get(url)
				d := time.Since(t)
				ok := err == nil && resp != nil && resp.StatusCode == http.StatusOK
				if resp != nil {
					resp.Body.Close()
				}
				mu.Lock()
				*results = append(*results, result{ok: ok, lat: d})
				mu.Unlock()
			}()
		}
		wg.Wait()
	}()
	return results
}

func report(label string, res *[]result) {
	total := len(*res)
	var ok int
	lats := make([]time.Duration, 0, total)
	for _, r := range *res {
		if r.ok {
			ok++
		}
		lats = append(lats, r.lat)
	}
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p := func(q float64) time.Duration {
		if len(lats) == 0 {
			return 0
		}
		return lats[int(float64(len(lats))*q)%len(lats)].Round(time.Millisecond)
	}
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(ok) / float64(total)
	}
	fmt.Printf("%-14s requests=%-5d succeeded=%d (%.1f%%)  p50=%-8s p99=%s\n", label+":", total, ok, pct, p(0.50), p(0.99))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func serve(mux *http.ServeMux) {
	addr := ":" + getenv("PORT", "8080")
	fmt.Printf("listening on %s as %s\n", addr, getenv("ROLE", "?"))
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func fetchText(client *http.Client, url string) string {
	resp, err := client.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
