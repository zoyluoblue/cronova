package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

// Deleting a DAG archives it: it is evicted from the cache (no longer
// triggerable/schedulable) and its YAML file is removed, while the row + history
// are preserved.
func TestDeleteDAGArchivesAndEvicts(t *testing.T) {
	dir := t.TempDir()
	s := newTestSchedulerWithDir(t, dir)
	ctx := context.Background()
	if _, err := s.CreateDAG(ctx, "dag_id: gone\ntasks:\n  - id: a\n    command: \"echo a\"\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gone.yaml")); err != nil {
		t.Fatalf("yaml should exist before delete: %v", err)
	}
	if err := s.DeleteDAG(ctx, "gone"); err != nil {
		t.Fatalf("DeleteDAG: %v", err)
	}
	// evicted from cache -> no longer triggerable
	if _, _, ok := s.cachedDAG("gone"); ok {
		t.Error("deleted DAG still in cache")
	}
	if _, err := s.TriggerManual(ctx, "gone", nil); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("trigger after delete = %v, want ErrNotFound", err)
	}
	// file removed, but the row (definition) is preserved for recovery
	if _, err := os.Stat(filepath.Join(dir, "gone.yaml")); !os.IsNotExist(err) {
		t.Errorf("yaml should be removed, stat err = %v", err)
	}
	if sd, err := s.store.GetDAG(ctx, "gone"); err != nil || sd.DefinitionYAML == "" {
		t.Errorf("row/definition should be preserved: dag=%v err=%v", sd, err)
	}
	// hidden from the active list
	dags, _ := s.store.ListDAGs(ctx)
	for _, d := range dags {
		if d.DagID == "gone" {
			t.Error("deleted DAG still listed")
		}
	}
}

// Deleting a DAG with a queued/running run is refused (avoids orphaning work).
func TestDeleteDAGRefusedWithActiveRuns(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "busy", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "echo a", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TriggerManual(ctx, "busy", nil); err != nil { // creates a queued run
		t.Fatal(err)
	}
	if err := s.DeleteDAG(ctx, "busy"); !errors.Is(err, model.ErrActiveRuns) {
		t.Errorf("delete with a queued run = %v, want ErrActiveRuns", err)
	}
	// still active (not deleted)
	if _, _, ok := s.cachedDAG("busy"); !ok {
		t.Error("DAG should remain after refused delete")
	}
}

// Deleting an absent DAG returns ErrNotFound.
func TestDeleteDAGNotFound(t *testing.T) {
	s := newTestScheduler(t)
	if err := s.DeleteDAG(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete missing = %v, want ErrNotFound", err)
	}
}

// A downstream whose trigger_after includes an ARCHIVED upstream must NOT fire
// (the dangling upstream is treated as never-satisfied), even though the
// archived upstream's historical success row still exists for the logical date.
func TestDeletedUpstreamBlocksDownstream(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	mk := func(id string, after []string) *model.DAG {
		return &model.DAG{
			DagID: id, MaxActiveRuns: 1, StartDate: time.Now().UTC(), TriggerAfter: after,
			Tasks: []model.Task{{ID: "t", Command: "echo t", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess}},
		}
	}
	for _, d := range []*model.DAG{mk("up_a", nil), mk("up_c", nil), mk("down_b", []string{"up_a", "up_c"})} {
		if err := s.registerDAG(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	logical := time.Now().UTC().Truncate(time.Second)
	success := func(dag string) *model.DagRun {
		r := &model.DagRun{RunID: dag + "__r", DagID: dag, LogicalDate: logical, State: model.RunQueued, TriggerType: model.TriggerSchedule}
		if err := s.store.CreateDagRun(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := s.store.UpdateDagRunState(ctx, r.RunID, model.RunSuccess, &logical, &logical); err != nil {
			t.Fatal(err)
		}
		r.State = model.RunSuccess
		return r
	}
	success("up_a")
	cRun := success("up_c")
	// Archive up_a (its run is terminal, so delete is allowed); up_a is now a
	// dangling upstream of down_b.
	if err := s.DeleteDAG(ctx, "up_a"); err != nil {
		t.Fatal(err)
	}
	// up_c's completion would normally fire down_b (both upstreams succeeded at
	// the same logical date) — but up_a is archived, so down_b must stay blocked.
	if deferred, err := s.triggerDownstreams(ctx, cRun); err != nil || deferred {
		t.Fatalf("triggerDownstreams: deferred=%v err=%v", deferred, err)
	}
	if runs, _ := s.store.ListDagRuns(ctx, "down_b", 10); len(runs) != 0 {
		t.Errorf("down_b should not fire with an archived upstream, got %d run(s)", len(runs))
	}
}
