package scheduler

import (
	"context"
	"testing"
)

// SetPaused must refresh the in-memory DAG cache, not just the store row — the
// tick reads d.Paused from the cache, so a store-only write left a "paused" DAG
// still being scheduled until the next reload.
func TestSetPausedRefreshesCache(t *testing.T) {
	dir := t.TempDir()
	s := newTestSchedulerWithDir(t, dir)
	ctx := context.Background()
	if _, err := s.CreateDAG(ctx, "dag_id: p\nschedule: \"* * * * *\"\ntasks:\n  - id: a\n    command: \"echo a\"\n"); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := s.cachedDAG("p"); d.Paused {
		t.Fatal("new dag should not be paused")
	}

	if err := s.SetPaused(ctx, "p", true); err != nil {
		t.Fatalf("SetPaused: %v", err)
	}
	if sd, _ := s.store.GetDAG(ctx, "p"); !sd.Paused {
		t.Error("store row not paused")
	}
	if d, _, _ := s.cachedDAG("p"); !d.Paused {
		t.Error("scheduler cache not refreshed after pause (the bug)")
	}

	if err := s.SetPaused(ctx, "p", false); err != nil {
		t.Fatalf("SetPaused unpause: %v", err)
	}
	if d, _, _ := s.cachedDAG("p"); d.Paused {
		t.Error("cache still paused after unpause")
	}
}

// collectSecrets must flag exactly connection passwords, so the executor's log
// sink is handed the right values to mask (see the executor redactWriter test for
// the masking itself).
func TestSecretRedactionHelpers(t *testing.T) {
	if !isSecretKey("conn.db.password") {
		t.Error("conn.*.password should be secret")
	}
	if isSecretKey("conn.db.host") || isSecretKey("var.token") || isSecretKey("params.x") {
		t.Error("only connection passwords should be treated as secret here")
	}

	var got []string
	resolve := func(k string) (string, bool) {
		switch k {
		case "conn.db.password":
			return "p@ss w0rd", true
		case "conn.db.host":
			return "db.local", true
		case "var.date":
			return "2026-07-09", true
		}
		return "", false
	}
	wrap := collectSecrets(resolve, &got)
	wrap("conn.db.password")
	wrap("conn.db.host")
	wrap("var.date")
	if len(got) != 1 || got[0] != "p@ss w0rd" {
		t.Fatalf("collected secrets = %v, want [\"p@ss w0rd\"]", got)
	}
}
