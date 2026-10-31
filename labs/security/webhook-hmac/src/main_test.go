package main

import (
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestNonceRetainedThroughFutureTimestampValidity(t *testing.T) {
	now := time.Unix(1700000000, 0)
	tolerance := 300 * time.Second
	timestamp := now.Add(299 * time.Second)
	body := []byte(`{"id":"future"}`)
	secret := []byte("secret")
	store := newNonceStore()
	req := httptest.NewRequest("POST", "/webhook", nil)
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	req.Header.Set(headerTimestamp, ts)
	req.Header.Set(headerNonce, "nonce")
	req.Header.Set(headerSignature, sign(secret, ts, "nonce", body))
	if err := verifyAt(req, body, secret, true, true, tolerance, store, now); err != nil {
		t.Fatal(err)
	}
	if err := verifyAt(req, body, secret, true, true, tolerance, store, now.Add(301*time.Second)); !errors.Is(err, errReplay) {
		t.Fatalf("replay accepted inside signature validity: %v", err)
	}
	if err := verifyAt(req, body, secret, true, true, tolerance, store, timestamp.Add(tolerance+time.Second)); err == nil {
		t.Fatal("expired timestamp accepted")
	}
}
func TestAmbiguousNonceRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	secret := []byte("secret")
	sig := sign(secret, ts, "a", []byte("0.1"))
	if sig != sign(secret, ts, "a.0", []byte("1")) {
		t.Fatal("counterexample changed")
	}
	req := httptest.NewRequest("POST", "/webhook", nil)
	req.Header.Set(headerTimestamp, ts)
	req.Header.Set(headerNonce, "a.0")
	req.Header.Set(headerSignature, sig)
	if err := verifyAt(req, []byte("1"), secret, true, true, time.Minute, newNonceStore(), now); err == nil {
		t.Fatal("ambiguous encoding accepted")
	}
}
func TestTamperedBodyRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	secret := []byte("secret")
	req := httptest.NewRequest("POST", "/webhook", nil)
	req.Header.Set(headerTimestamp, ts)
	req.Header.Set(headerNonce, "nonce")
	req.Header.Set(headerSignature, sign(secret, ts, "nonce", []byte("original")))
	if err := verifyAt(req, []byte("changed"), secret, true, true, time.Minute, newNonceStore(), now); err == nil {
		t.Fatal("tamper accepted")
	}
}

func TestNonceProtectionRequiresFreshness(t *testing.T) {
	req := httptest.NewRequest("POST", "/webhook", nil)
	if err := verifyAt(req, nil, nil, false, true, time.Minute, newNonceStore(), time.Now()); err == nil {
		t.Fatal("unbounded signature validity with finite nonce retention accepted")
	}
}
