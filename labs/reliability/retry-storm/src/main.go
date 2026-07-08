// Retry-storm lab: a real HTTP request chain client -> edge -> api -> backend.
//
// One binary, four roles (-role). edge and api are retrying proxies; backend is
// a tunable dependency that can be pushed into a degraded state; load drives the
// chain and reports the amplification factor (backend calls / client requests).
//
// Retry policy is read from the environment so the same image runs the naive and
// the fixed scenario with only compose overrides changing:
//
//	RETRY_ATTEMPTS  total attempts per hop (1 = no retry)
//	BACKOFF_MS      base backoff between attempts
//	JITTER          full|none  (full = randomize each backoff across [0, backoff])
//	ATTEMPT_TIMEOUT ms budget for a single downstream attempt
//	DEADLINE_MS     end-to-end budget propagated via the X-Deadline-Ms header;
//	                a hop refuses to start an attempt (or a retry) once the
//	                remaining budget is gone
//	RETRY_BUDGET    max retries as a fraction of requests (0 disables the cap);
//	                a token bucket, the Google SRE "retry budget"
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
	case "edge", "api":
		runProxy(role)
	case "load":
		runLoad()
	default:
		fmt.Fprintf(os.Stderr, "unknown ROLE %q\n", role)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// backend: the dependency everything ultimately calls.
// ---------------------------------------------------------------------------

func runBackend() {
	var received atomic.Int64 // every inbound attempt, including retries
	baseLatency := time.Duration(getenvInt("LATENCY_MS", 5)) * time.Millisecond
	// Capacity is a request RATE (req/s). Retries are sequential within a
	// request, so what they amplify is the arrival rate at the backend, not
	// peak concurrency. Past capacity, latency and failure rise with the
	// overload, so a retry-driven rate spike feeds on itself: a metastable loop.
	capacity := float64(getenvInt("CAPACITY", 220))
	degradedCapacity := float64(getenvInt("DEGRADED_CAPACITY", 90))
	// A degraded backend also has a base error rate independent of load. This
	// is the spark: it makes callers retry even before they have overloaded
	// anything. Whether those retries put out the fire or pour fuel on it is
	// the whole lesson.
	degradedBaseFail := getenvFloat("DEGRADED_FAIL", 0.4)
	degraded := &atomic.Bool{}

	// Sliding-window request-rate estimate: ten 100ms buckets summed give the
	// arrivals over the last ~1s. A ticker advances and clears buckets.
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
			// Steep penalty: once overloaded, the backend gets slow enough that
			// callers time out, so extra attempts stop helping and only add
			// load. This is what turns amplification into collapse.
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
// proxy (edge, api): retries its single downstream with the configured policy.
// ---------------------------------------------------------------------------

type policy struct {
	attempts       int
	backoff        time.Duration
	jitter         bool
	attemptTimeout time.Duration
	deadline       time.Duration // 0 = no end-to-end budget
	retryBudget    float64       // 0 = unlimited retries
}

func loadPolicy() policy {
	return policy{
		attempts:       getenvInt("RETRY_ATTEMPTS", 1),
		backoff:        time.Duration(getenvInt("BACKOFF_MS", 20)) * time.Millisecond,
		jitter:         getenv("JITTER", "none") == "full",
		attemptTimeout: time.Duration(getenvInt("ATTEMPT_TIMEOUT", 200)) * time.Millisecond,
		deadline:       time.Duration(getenvInt("DEADLINE_MS", 0)) * time.Millisecond,
		retryBudget:    getenvFloat("RETRY_BUDGET", 0),
	}
}

// retryBucket caps retries as a fraction of requests. Every request adds one
// token; every retry spends `1/ratio` tokens. When the bucket is empty, retries
// are refused, so a storm cannot grow retries without bound.
type retryBucket struct {
	mu     sync.Mutex
	tokens float64
	ratio  float64
	max    float64
}

func newRetryBucket(ratio float64) *retryBucket {
	if ratio <= 0 {
		return nil
	}
	return &retryBucket{ratio: ratio, max: 100, tokens: 100}
}

func (b *retryBucket) request() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.tokens += b.ratio
	if b.tokens > b.max {
		b.tokens = b.max
	}
	b.mu.Unlock()
}

func (b *retryBucket) allowRetry() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

func runProxy(role string) {
	pol := loadPolicy()
	downstream := getenv("DOWNSTREAM", "")
	client := &http.Client{}
	bucket := newRetryBucket(pol.retryBudget)

	mux := http.NewServeMux()
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		bucket.request()

		// End-to-end deadline: use the caller's if present, else start ours.
		remaining := pol.deadline
		if hdr := r.Header.Get("X-Deadline-Ms"); hdr != "" {
			if ms, err := strconv.Atoi(hdr); err == nil {
				remaining = time.Duration(ms) * time.Millisecond
			}
		}
		start := time.Now()

		var lastCode int
		for attempt := 1; attempt <= pol.attempts; attempt++ {
			if attempt > 1 {
				// Budget check: do not retry if the end-to-end time is spent.
				if pol.deadline > 0 && time.Since(start) >= remaining {
					break
				}
				if !bucket.allowRetry() {
					break
				}
				sleep := pol.backoff * time.Duration(1<<(attempt-2)) // exponential
				if pol.jitter {
					sleep = time.Duration(rand.Int64N(int64(sleep) + 1)) // full jitter
				}
				time.Sleep(sleep)
			}

			budgetLeft := remaining - time.Since(start)
			code, err := callDownstream(client, downstream, pol, remaining, budgetLeft, start)
			if err == nil && code == http.StatusOK {
				io.WriteString(w, role+":ok")
				return
			}
			lastCode = code
		}
		if lastCode == 0 {
			lastCode = http.StatusServiceUnavailable
		}
		http.Error(w, role+":downstream failed", lastCode)
	})
	serve(mux)
}

func callDownstream(client *http.Client, downstream string, pol policy, remaining, budgetLeft time.Duration, start time.Time) (int, error) {
	timeout := pol.attemptTimeout
	// If an end-to-end budget is set, never wait longer than what is left.
	if pol.deadline > 0 {
		if budgetLeft <= 0 {
			return http.StatusGatewayTimeout, fmt.Errorf("budget exhausted")
		}
		if budgetLeft < timeout {
			timeout = budgetLeft
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, downstream+"/work", nil)
	if pol.deadline > 0 {
		// Propagate the shrinking budget so the next hop sees the truth.
		pass := remaining - time.Since(start)
		req.Header.Set("X-Deadline-Ms", strconv.Itoa(int(pass.Milliseconds())))
	}
	resp, err := client.Do(req)
	if err != nil {
		return http.StatusGatewayTimeout, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// ---------------------------------------------------------------------------
// load: drive the chain and report amplification.
// ---------------------------------------------------------------------------

func runLoad() {
	target := getenv("TARGET", "http://edge:8080")
	backend := getenv("BACKEND", "http://backend:8080")
	// Open-loop: fire at a fixed arrival rate regardless of how slow responses
	// get. This is the honest model for user traffic, and the only one that
	// shows a retry storm: closed-loop workers self-throttle when latency
	// climbs, hiding the collapse. The offered rate is set BELOW the degraded
	// backend's capacity, so only amplification can push it over the edge.
	rate := getenvInt("RATE", 70)
	duration := time.Duration(getenvInt("DURATION_S", 20)) * time.Second
	label := getenv("LABEL", "run")

	client := &http.Client{Timeout: 10 * time.Second}
	http.Get(backend + "/reset")
	http.Get(backend + "/degrade?on=true")
	fmt.Printf("[%s] backend degraded; offering %d req/s for %s\n", label, rate, duration)

	var ok, fail atomic.Int64
	var mu sync.Mutex
	var lats []time.Duration
	// Bound in-flight so a total stall cannot exhaust memory; a full guard
	// counts as a client-side failure, which is itself a load-shedding signal.
	guard := make(chan struct{}, 4000)

	var wg sync.WaitGroup
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	defer ticker.Stop()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		<-ticker.C
		select {
		case guard <- struct{}{}:
		default:
			fail.Add(1)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-guard }()
			t := time.Now()
			resp, err := client.Get(target + "/work")
			d := time.Since(t)
			if err == nil && resp.StatusCode == http.StatusOK {
				ok.Add(1)
				resp.Body.Close()
			} else {
				fail.Add(1)
				if resp != nil {
					resp.Body.Close()
				}
			}
			mu.Lock()
			lats = append(lats, d)
			mu.Unlock()
		}()
	}
	wg.Wait()

	backendCalls := fetchInt(client, backend+"/stats")
	http.Get(backend + "/degrade?on=false")

	total := ok.Load() + fail.Load()
	sort.Slice(lats, func(i, j int) bool { return lats[i] < lats[j] })
	p := func(q float64) time.Duration {
		if len(lats) == 0 {
			return 0
		}
		return lats[int(float64(len(lats))*q)%len(lats)].Round(time.Millisecond)
	}
	amp := 0.0
	if total > 0 {
		amp = float64(backendCalls) / float64(total)
	}
	successPct := 0.0
	if total > 0 {
		successPct = 100 * float64(ok.Load()) / float64(total)
	}

	fmt.Printf("\n=== %s ===\n", label)
	fmt.Printf("client requests:   %d\n", total)
	fmt.Printf("  succeeded:       %d (%.1f%%)\n", ok.Load(), successPct)
	fmt.Printf("  failed:          %d\n", fail.Load())
	fmt.Printf("backend calls:     %d\n", backendCalls)
	fmt.Printf("AMPLIFICATION:     %.2fx  (backend calls / client requests)\n", amp)
	fmt.Printf("client latency:    p50=%s p99=%s\n", p(0.50), p(0.99))
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

func fetchInt(client *http.Client, url string) int64 {
	resp, err := client.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	n, _ := strconv.ParseInt(string(b), 10, 64)
	return n
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
