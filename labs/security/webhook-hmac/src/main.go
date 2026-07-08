package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	headerTimestamp = "X-Webhook-Timestamp"
	headerNonce     = "X-Webhook-Nonce"
	headerSignature = "X-Webhook-Signature"
)

func main() {
	switch getenv("ROLE", "server") {
	case "server":
		runServer()
	case "client":
		runClient()
	default:
		fmt.Fprintf(os.Stderr, "unknown ROLE\n")
		os.Exit(1)
	}
}

type nonceStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	nonces   map[string]time.Time
	accepted atomic.Int64
	rejected atomic.Int64
}

func newNonceStore(ttl time.Duration) *nonceStore {
	return &nonceStore{ttl: ttl, nonces: map[string]time.Time{}}
}

func (s *nonceStore) seenBefore(nonce string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, expiresAt := range s.nonces {
		if now.After(expiresAt) {
			delete(s.nonces, n)
		}
	}
	if _, ok := s.nonces[nonce]; ok {
		return true
	}
	s.nonces[nonce] = now.Add(s.ttl)
	return false
}

func runServer() {
	secret := []byte(getenv("WEBHOOK_SECRET", "whsec_demo_secret_32_bytes_minimum"))
	verifyTimestamp := getenvBool("VERIFY_TIMESTAMP", true)
	verifyNonce := getenvBool("VERIFY_NONCE", true)
	tolerance := time.Duration(getenvInt("TOLERANCE_S", 300)) * time.Second
	store := newNonceStore(tolerance)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "accepted=%d rejected=%d nonces=%d\n", store.accepted.Load(), store.rejected.Load(), len(store.nonces))
	})
	mux.HandleFunc("/webhook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if err := verify(r, body, secret, verifyTimestamp, verifyNonce, tolerance, store); err != nil {
			store.rejected.Add(1)
			status := http.StatusUnauthorized
			if errors.Is(err, errReplay) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		store.accepted.Add(1)
		fmt.Fprintf(w, "accepted event: %s\n", eventID(body))
	})
	serve(mux)
}

var errReplay = errors.New("replay detected")

func verify(r *http.Request, body, secret []byte, verifyTimestamp, verifyNonce bool, tolerance time.Duration, store *nonceStore) error {
	tsText := r.Header.Get(headerTimestamp)
	nonce := r.Header.Get(headerNonce)
	got := r.Header.Get(headerSignature)
	if tsText == "" || nonce == "" || got == "" {
		return errors.New("missing signature headers")
	}

	ts, err := strconv.ParseInt(tsText, 10, 64)
	if err != nil {
		return errors.New("bad timestamp")
	}
	if verifyTimestamp {
		signedAt := time.Unix(ts, 0)
		age := time.Since(signedAt)
		if age < -tolerance || age > tolerance {
			return errors.New("timestamp outside tolerance")
		}
	}

	expected := sign(secret, tsText, nonce, body)
	if !constantTimeSignatureEqual(got, expected) {
		return errors.New("bad signature")
	}
	if verifyNonce && store.seenBefore(nonce, time.Now()) {
		return errReplay
	}
	return nil
}

func sign(secret []byte, timestamp, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte(nonce))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func constantTimeSignatureEqual(gotHeader, expectedHeader string) bool {
	gotHex := strings.TrimPrefix(gotHeader, "v1=")
	expectedHex := strings.TrimPrefix(expectedHeader, "v1=")
	got, err1 := hex.DecodeString(gotHex)
	expected, err2 := hex.DecodeString(expectedHex)
	if err1 != nil || err2 != nil || len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare(got, expected) == 1
}

func eventID(body []byte) string {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.ID == "" {
		return "unknown"
	}
	return payload.ID
}

func runClient() {
	target := getenv("TARGET", "http://localhost:8080")
	secret := []byte(getenv("WEBHOOK_SECRET", "whsec_demo_secret_32_bytes_minimum"))
	scenario := getenv("SCENARIO", "valid")
	client := &http.Client{Timeout: 5 * time.Second}

	var err error
	switch scenario {
	case "valid":
		err = expect(client, target, secret, "valid", delivery{body: payload("evt-valid"), want: http.StatusOK})
	case "tamper":
		err = expect(client, target, secret, "tamper", delivery{body: payload("evt-tamper"), sendBody: payload("evt-tampered"), want: http.StatusUnauthorized})
	case "stale":
		err = expect(client, target, secret, "stale", delivery{body: payload("evt-stale"), timestamp: time.Now().Add(-10 * time.Minute), want: http.StatusUnauthorized})
	case "replay":
		err = replay(client, target, secret, "replay", http.StatusOK, http.StatusConflict)
	case "replay-vulnerable":
		err = replay(client, target, secret, "replay-vulnerable", http.StatusOK, http.StatusOK)
	case "all":
		if err = expect(client, target, secret, "valid", delivery{body: payload("evt-valid"), want: http.StatusOK}); err == nil {
			err = expect(client, target, secret, "tamper", delivery{body: payload("evt-tamper"), sendBody: payload("evt-tampered"), want: http.StatusUnauthorized})
		}
		if err == nil {
			err = expect(client, target, secret, "stale", delivery{body: payload("evt-stale"), timestamp: time.Now().Add(-10 * time.Minute), want: http.StatusUnauthorized})
		}
		if err == nil {
			err = replay(client, target, secret, "replay", http.StatusOK, http.StatusConflict)
		}
	default:
		err = fmt.Errorf("unknown SCENARIO %q", scenario)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type delivery struct {
	body      []byte
	sendBody  []byte
	timestamp time.Time
	nonce     string
	want      int
}

func expect(client *http.Client, target string, secret []byte, label string, d delivery) error {
	code, response, err := send(client, target, secret, d)
	if err != nil {
		return err
	}
	fmt.Printf("%-18s got=%d want=%d %s", label+":", code, d.want, response)
	if code != d.want {
		return fmt.Errorf("%s failed: got %d want %d", label, code, d.want)
	}
	return nil
}

func replay(client *http.Client, target string, secret []byte, label string, firstWant, secondWant int) error {
	d := delivery{
		body:      payload("evt-replay"),
		timestamp: time.Now(),
		nonce:     "nonce-replay-fixed",
		want:      firstWant,
	}
	if err := expect(client, target, secret, label+" first", d); err != nil {
		return err
	}
	d.want = secondWant
	return expect(client, target, secret, label+" second", d)
}

func send(client *http.Client, target string, secret []byte, d delivery) (int, string, error) {
	if d.timestamp.IsZero() {
		d.timestamp = time.Now()
	}
	if d.nonce == "" {
		d.nonce = randomNonce()
	}
	if d.sendBody == nil {
		d.sendBody = d.body
	}
	ts := strconv.FormatInt(d.timestamp.Unix(), 10)
	sig := sign(secret, ts, d.nonce, d.body)

	req, err := http.NewRequest(http.MethodPost, target+"/webhook", bytes.NewReader(d.sendBody))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(headerTimestamp, ts)
	req.Header.Set(headerNonce, d.nonce)
	req.Header.Set(headerSignature, sig)

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

func payload(id string) []byte {
	return []byte(fmt.Sprintf(`{"id":%q,"type":"payment.succeeded","amount_cents":1299}`, id))
}

func randomNonce() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func serve(handler http.Handler) {
	server := &http.Server{Addr: ":8080", Handler: handler}
	fmt.Println("listening on :8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v == "true" || v == "1" || v == "yes"
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			panic(err)
		}
		return n
	}
	return fallback
}
