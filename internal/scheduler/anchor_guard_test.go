package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// Guards for the two review findings: (1) a flood of backfill runs must not
// crowd the catchup anchor out of view; (2) retention pruning must never
// delete a DAG's newest scheduled run (the anchor row).
func TestScheduleAnchorSurvivesBackfillAndPrune(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	if _, err := s.CreateDAG(ctx, `
dag_id: anchor
schedule: "0 * * * *"
start_date: 2020-01-01
catchup: true
tasks:
  - id: t1
    command: echo hi
`); err != nil {
		t.Fatal(err)
	}
	d, _, _ := s.cachedDAG("anchor")

	// One old scheduled run = the anchor (finished long before any cutoff).
	anchorDate := time.Now().UTC().Add(-200 * 24 * time.Hour).Truncate(time.Hour)
	old := anchorDate.Add(time.Minute)
	if err := s.store.CreateDagRun(ctx, &model.DagRun{RunID: "anchor__sched", DagID: "anchor",
		LogicalDate: anchorDate, State: model.RunQueued, TriggerType: model.TriggerSchedule}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpdateDagRunState(ctx, "anchor__sched", model.RunSuccess, &old, &old); err != nil {
		t.Fatal(err)
	}

	// (1) 150 newer backfill runs — more than any listing window.
	from := time.Now().UTC().Add(-160 * time.Hour).Truncate(time.Hour)
	to := time.Now().UTC().Add(-10 * time.Hour)
	created, _, err := s.Backfill(ctx, "anchor", from, to)
	if err != nil || created < 120 {
		t.Fatalf("backfill created=%d err=%v, want ≥120", created, err)
	}
	if got := s.scheduleAnchor(ctx, d); !got.Equal(anchorDate) {
		t.Fatalf("anchor after backfill flood = %s, want %s (crowded out!)", got, anchorDate)
	}

	// (2) prune with a cutoff far past the anchor run's finished_at: the anchor
	// row must survive; the backfill rows are still queued so they survive too.
	pruned, err := s.store.PruneRuns(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range pruned {
		if r.RunID == "anchor__sched" {
			t.Fatal("retention pruned the newest scheduled run (the catchup anchor)")
		}
	}
	if got := s.scheduleAnchor(ctx, d); !got.Equal(anchorDate) {
		t.Fatalf("anchor after prune = %s, want %s", got, anchorDate)
	}
}

// max_active_runs must gate the queued→running promotion: a backfill of N
// periods on a max_active_runs=1 DAG executes one run at a time.
func TestQueuedRunsGatedByMaxActiveRuns(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	if _, err := s.CreateDAG(ctx, `
dag_id: gated
schedule: "0 * * * *"
start_date: 2026-01-01
max_active_runs: 1
tasks:
  - id: t1
    command: sleep 30
`); err != nil {
		t.Fatal(err)
	}
	from := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Hour)
	created, _, err := s.Backfill(ctx, "gated", from, time.Now().UTC())
	if err != nil || created < 3 {
		t.Fatalf("backfill created=%d err=%v", created, err)
	}
	s.tickOnce(ctx)
	runs, _ := s.store.ListDagRuns(ctx, "gated", 100)
	var running, queued int
	for _, r := range runs {
		switch r.State {
		case model.RunRunning:
			running++
		case model.RunQueued:
			queued++
		}
	}
	if running != 1 || queued != created-1 {
		t.Fatalf("after one tick: running=%d queued=%d (created=%d), want exactly 1 running", running, queued, created)
	}
}
