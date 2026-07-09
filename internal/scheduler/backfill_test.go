package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// Backfill enqueues one queued run per schedule period in the window, skips
// periods that already have runs, refuses schedule-less DAGs, and clamps the
// window to now (never enqueues future periods).
func TestBackfill(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	yaml := `
dag_id: bf
schedule: "0 * * * *"
start_date: 2026-01-01
tasks:
  - id: t1
    command: echo hi
`
	if _, err := s.CreateDAG(ctx, yaml); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	from := now.Add(-3 * time.Hour).Truncate(time.Hour)
	to := now // hourly schedule → expect the 3 (or 4) whole hours in the window

	created, skipped, err := s.Backfill(ctx, "bf", from, to)
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if skipped != 0 || created < 3 || created > 4 {
		t.Fatalf("created=%d skipped=%d, want 3-4 created, 0 skipped", created, skipped)
	}
	runs, _ := s.store.ListDagRuns(ctx, "bf", 100)
	for _, r := range runs {
		if r.TriggerType != model.TriggerBackfill || r.State != model.RunQueued {
			t.Fatalf("run %s: trigger=%s state=%s, want backfill/queued", r.RunID, r.TriggerType, r.State)
		}
		if r.LogicalDate.After(now) {
			t.Fatalf("run %s has a future logical date %s", r.RunID, r.LogicalDate)
		}
	}

	// idempotent: same window again → all skipped
	c2, s2, err := s.Backfill(ctx, "bf", from, to)
	if err != nil || c2 != 0 || s2 != created {
		t.Fatalf("second backfill = created %d skipped %d err %v; want 0/%d/nil", c2, s2, err, created)
	}

	// future window clamps to now → empty → error
	if _, _, err := s.Backfill(ctx, "bf", now.Add(time.Hour), now.Add(3*time.Hour)); err == nil {
		t.Fatal("future-only window should error (empty after clamping)")
	}

	// schedule-less DAG is rejected with a clear message
	if _, err := s.CreateDAG(ctx, "dag_id: manual_only\ntasks:\n  - id: t\n    command: echo x\n"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Backfill(ctx, "manual_only", from, to); err == nil || !strings.Contains(err.Error(), "no schedule") {
		t.Fatalf("schedule-less backfill err = %v, want 'no schedule'", err)
	}
}
