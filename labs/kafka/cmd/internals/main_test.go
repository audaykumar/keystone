package main

import "testing"

func TestAuditSeparatesFailedSendsFromAckedLoss(t *testing.T) {
	run := runManifest{Acked: []int64{0, 2}, Failed: []int64{1, 3}, Attempted: 4}
	missing, dups, failedPresent := classify(run, map[int64]int{0: 1, 1: 1, 2: 2})
	if len(missing) != 0 || dups != 1 || failedPresent != 1 {
		t.Fatalf("incorrect classification: %v %d %d", missing, dups, failedPresent)
	}
	missing, _, _ = classify(run, map[int64]int{0: 1})
	if len(missing) != 1 || missing[0] != 2 {
		t.Fatalf("must report only absent acknowledged ID: %v", missing)
	}
}
