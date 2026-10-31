package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInheritedDeadlineWithNoLocalDefault(t *testing.T) {
	var calls atomic.Int64
	cancelled := make(chan struct{}, 10)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
		cancelled <- struct{}{}
	}))
	defer backend.Close()
	handler := proxyHandler("test", policy{attempts: 3, attemptTimeout: time.Second, backoff: time.Second}, backend.URL, &http.Client{}, nil)
	req := httptest.NewRequest("GET", "/work", nil)
	req.Header.Set("X-Deadline-Ms", "30")
	start := time.Now()
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("inherited budget ignored")
	}
	if calls.Load() != 1 {
		t.Fatalf("expired retry sent: calls=%d", calls.Load())
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("backend not cancelled")
	}
}
func TestCallerCancellationStopsDownstream(t *testing.T) {
	reached := make(chan struct{})
	cancelled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(reached); <-r.Context().Done(); close(cancelled) }))
	defer backend.Close()
	handler := proxyHandler("test", policy{attempts: 3, attemptTimeout: time.Second}, backend.URL, &http.Client{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/work", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	<-reached
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler ignored cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("backend ignored cancellation")
	}
}

func TestExpiredInheritedBudgetSendsNoWork(t *testing.T) {
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer backend.Close()
	for _, budget := range []string{"0", "-1"} {
		req := httptest.NewRequest("GET", "/work", nil)
		req.Header.Set("X-Deadline-Ms", budget)
		response := httptest.NewRecorder()
		proxyHandler("test", policy{attempts: 3, attemptTimeout: time.Second}, backend.URL, &http.Client{}, nil).ServeHTTP(response, req)
		if response.Code != http.StatusGatewayTimeout {
			t.Fatalf("expired %s: status %d", budget, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("expired work reached downstream")
	}
}

func TestControlFailureIsNotRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 500) }))
	defer server.Close()
	if err := control(server.Client(), server.URL); err == nil {
		t.Fatal("failed control request accepted")
	}
}
