package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// PruneRuns must delete only finished runs older than the cutoff — never
// active runs (any age) and never recently finished ones — and must cascade to
// their task instances.
func TestPruneRuns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertDAG(ctx, &model.DAG{DagID: "p", MaxActiveRuns: 1, DefinitionYAML: "dag_id: p"}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	mk := func(id string, state model.RunState, finished *time.Time, ld time.Time) {
		t.Helper()
		if err := s.CreateDagRun(ctx, &model.DagRun{RunID: id, DagID: "p", LogicalDate: ld,
			State: model.RunQueued, TriggerType: model.TriggerManual}); err != nil {
			t.Fatal(err)
		}
		if state != model.RunQueued {
			if err := s.UpdateDagRunState(ctx, id, state, &old, finished); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CreateTaskInstance(ctx, &model.TaskInstance{RunID: id, TaskID: "t1", State: model.TaskSuccess}); err != nil {
			t.Fatal(err)
		}
	}
	mk("p__old_ok", model.RunSuccess, &old, now.Add(-4*24*time.Hour))  // prune
	mk("p__old_fail", model.RunFailed, &old, now.Add(-3*24*time.Hour)) // prune
	mk("p__fresh", model.RunSuccess, &now, now.Add(-2*24*time.Hour))   // keep: finished recently
	mk("p__running", model.RunRunning, nil, now.Add(-1*24*time.Hour))  // keep: still active, even though old
	mk("p__queued", model.RunQueued, nil, now)                         // keep: queued

	pruned, err := s.PruneRuns(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneRuns: %v", err)
	}
	got := map[string]bool{}
	for _, r := range pruned {
		got[r.RunID] = true
		if r.DagID != "p" {
			t.Errorf("pruned run %s has DagID %q, want p", r.RunID, r.DagID)
		}
	}
	if len(got) != 2 || !got["p__old_ok"] || !got["p__old_fail"] {
		t.Fatalf("pruned = %v, want exactly {p__old_ok, p__old_fail}", got)
	}

	// deleted rows are gone, survivors remain — including their task instances
	for id, want := range map[string]bool{"p__old_ok": false, "p__fresh": true, "p__running": true, "p__queued": true} {
		_, err := s.GetDagRun(ctx, id)
		if want && err != nil {
			t.Errorf("run %s should survive: %v", id, err)
		}
		if !want && err == nil {
			t.Errorf("run %s should be deleted", id)
		}
		tis, _ := s.ListTaskInstances(ctx, id)
		if want && len(tis) != 1 {
			t.Errorf("run %s task instances = %d, want 1", id, len(tis))
		}
		if !want && len(tis) != 0 {
			t.Errorf("run %s task instances should be pruned, got %d", id, len(tis))
		}
	}

	// idempotent: nothing left to prune
	again, err := s.PruneRuns(ctx, now.Add(-24*time.Hour))
	if err != nil || len(again) != 0 {
		t.Fatalf("second prune = %v, %v; want empty, nil", again, err)
	}
}
