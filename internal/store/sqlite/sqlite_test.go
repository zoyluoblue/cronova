package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func TestMigrateSeedsDefaultPool(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	p, err := s.GetPool(ctx, model.DefaultPoolName)
	if err != nil {
		t.Fatalf("GetPool: %v", err)
	}
	if p.Slots != 16 {
		t.Errorf("default pool slots = %d, want 16", p.Slots)
	}
	// Migrate is idempotent.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

func TestDAGRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := &model.DAG{
		DagID:          "daily_etl",
		Schedule:       "0 2 * * *",
		StartDate:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Catchup:        true,
		MaxActiveRuns:  1,
		DefinitionYAML: "dag_id: daily_etl",
		Owner:          "alice",
	}
	if err := s.UpsertDAG(ctx, d); err != nil {
		t.Fatalf("UpsertDAG: %v", err)
	}
	got, err := s.GetDAG(ctx, "daily_etl")
	if err != nil {
		t.Fatalf("GetDAG: %v", err)
	}
	if got.Schedule != d.Schedule || !got.Catchup || got.Owner != "alice" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if !got.StartDate.Equal(d.StartDate) {
		t.Errorf("StartDate = %v, want %v", got.StartDate, d.StartDate)
	}

	// Upsert again updates in place.
	d.Schedule = "0 3 * * *"
	if err := s.UpsertDAG(ctx, d); err != nil {
		t.Fatalf("UpsertDAG update: %v", err)
	}
	got, _ = s.GetDAG(ctx, "daily_etl")
	if got.Schedule != "0 3 * * *" {
		t.Errorf("schedule not updated: %q", got.Schedule)
	}

	if err := s.SetDAGPaused(ctx, "daily_etl", true); err != nil {
		t.Fatalf("SetDAGPaused: %v", err)
	}
	got, _ = s.GetDAG(ctx, "daily_etl")
	if !got.Paused {
		t.Error("expected paused")
	}

	if _, err := s.GetDAG(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// SoftDeleteDAG hides a DAG from ListDAGs but keeps the row (GetDAG still
// returns it); re-creating the id (UpsertDAG) revives it.
func TestSoftDeleteDAG(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := &model.DAG{DagID: "sd", MaxActiveRuns: 1, DefinitionYAML: "dag_id: sd"}
	if err := s.UpsertDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	listed := func() bool {
		dags, _ := s.ListDAGs(ctx)
		for _, x := range dags {
			if x.DagID == "sd" {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("sd should be listed before delete")
	}
	if err := s.SoftDeleteDAG(ctx, "sd"); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Error("sd should be hidden from ListDAGs after soft delete")
	}
	if got, err := s.GetDAG(ctx, "sd"); err != nil || got.DefinitionYAML == "" {
		t.Errorf("GetDAG should still return the archived row: %v %v", got, err)
	}
	// Deleting again is a no-op error (already deleted).
	if err := s.SoftDeleteDAG(ctx, "sd"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("re-delete = %v, want ErrNotFound", err)
	}
	// Re-creating the id revives it (deleted_at cleared on upsert).
	if err := s.UpsertDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Error("re-created sd should be listed again")
	}
}

// CreateDagRun must refuse to insert a run for a soft-deleted DAG — this closes
// the check-then-act window where a concurrent scheduler tick could create (and
// then execute) a run for a DAG that was just archived.
func TestCreateDagRunRefusedForDeletedDAG(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertDAG(ctx, &model.DAG{DagID: "x", MaxActiveRuns: 1, DefinitionYAML: "dag_id: x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SoftDeleteDAG(ctx, "x"); err != nil {
		t.Fatal(err)
	}
	err := s.CreateDagRun(ctx, &model.DagRun{RunID: "x__r1", DagID: "x", LogicalDate: time.Now().UTC(), State: model.RunQueued, TriggerType: model.TriggerSchedule})
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("CreateDagRun for a soft-deleted DAG = %v, want ErrNotFound", err)
	}
	if runs, _ := s.ListDagRuns(ctx, "x", 10); len(runs) != 0 {
		t.Errorf("no run should be created for a deleted DAG, got %d", len(runs))
	}
}

// A re-upsert (DAG edit via the YAML build path, or a restart re-registering a
// file DAG) must NOT clobber operational columns the YAML cannot carry: paused,
// owner, project. Otherwise editing a paused DAG silently resumes it.
func TestUpsertPreservesOperationalState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	d := &model.DAG{DagID: "ops", MaxActiveRuns: 1, DefinitionYAML: "dag_id: ops", Owner: "alice", Project: "p1"}
	if err := s.UpsertDAG(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDAGPaused(ctx, "ops", true); err != nil {
		t.Fatal(err)
	}
	// Re-register as the build path would: Paused=false, Owner/Project empty.
	edited := &model.DAG{DagID: "ops", MaxActiveRuns: 2, DefinitionYAML: "dag_id: ops\nmax_active_runs: 2", Paused: false, Owner: "", Project: ""}
	if err := s.UpsertDAG(ctx, edited); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetDAG(ctx, "ops")
	if !got.Paused {
		t.Error("paused was clobbered by re-upsert (would silently resume the DAG)")
	}
	if got.Owner != "alice" || got.Project != "p1" {
		t.Errorf("owner/project clobbered: owner=%q project=%q", got.Owner, got.Project)
	}
	if got.MaxActiveRuns != 2 {
		t.Errorf("definition field not updated: max_active_runs=%d, want 2", got.MaxActiveRuns)
	}
}

func TestDagRunUniqueLogicalDate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustDAG(t, s, "etl")

	ld := time.Date(2026, 6, 9, 2, 0, 0, 0, time.UTC)
	run := &model.DagRun{
		RunID:       "etl__2026-06-09",
		DagID:       "etl",
		LogicalDate: ld,
		State:       model.RunQueued,
		TriggerType: model.TriggerSchedule,
	}
	if err := s.CreateDagRun(ctx, run); err != nil {
		t.Fatalf("CreateDagRun: %v", err)
	}
	// Same (dag_id, logical_date) -> ErrAlreadyExists. This is the catchup
	// dedup guarantee.
	dup := &model.DagRun{
		RunID:       "etl__dup",
		DagID:       "etl",
		LogicalDate: ld,
		State:       model.RunQueued,
		TriggerType: model.TriggerSchedule,
	}
	if err := s.CreateDagRun(ctx, dup); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	got, err := s.GetDagRunByLogicalDate(ctx, "etl", ld)
	if err != nil {
		t.Fatalf("GetDagRunByLogicalDate: %v", err)
	}
	if got.RunID != "etl__2026-06-09" {
		t.Errorf("got run %q", got.RunID)
	}

	now := time.Now().UTC()
	if err := s.UpdateDagRunState(ctx, run.RunID, model.RunRunning, &now, nil); err != nil {
		t.Fatalf("UpdateDagRunState: %v", err)
	}
	n, err := s.CountActiveRuns(ctx, "etl")
	if err != nil {
		t.Fatalf("CountActiveRuns: %v", err)
	}
	if n != 1 {
		t.Errorf("active runs = %d, want 1", n)
	}
}

func TestTaskInstanceLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustDAG(t, s, "etl")
	run := &model.DagRun{RunID: "etl__r1", DagID: "etl", LogicalDate: time.Now().UTC(), State: model.RunQueued, TriggerType: model.TriggerManual}
	if err := s.CreateDagRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	ti := &model.TaskInstance{
		RunID: "etl__r1", TaskID: "extract", State: model.TaskScheduled,
		MaxRetries: 2, Pool: model.DefaultPoolName, Priority: 5,
	}
	if err := s.CreateTaskInstance(ctx, ti); err != nil {
		t.Fatalf("CreateTaskInstance: %v", err)
	}
	if ti.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	// Duplicate (run_id, task_id) rejected.
	dup := &model.TaskInstance{RunID: "etl__r1", TaskID: "extract", State: model.TaskScheduled, Pool: model.DefaultPoolName}
	if err := s.CreateTaskInstance(ctx, dup); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	// Move scheduled -> queued -> running and check pool occupancy.
	ti.State = model.TaskQueued
	if err := s.UpdateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}
	n, err := s.CountRunningInPool(ctx, model.DefaultPoolName)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("pool occupancy = %d, want 1 (queued counts)", n)
	}

	now := time.Now().UTC()
	ti.State = model.TaskRunning
	ti.ExecutorRef = "ref-123"
	ti.StartedAt = &now
	if err := s.UpdateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTaskInstance(ctx, ti.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != model.TaskRunning || got.ExecutorRef != "ref-123" || got.StartedAt == nil {
		t.Errorf("unexpected ti: %+v", got)
	}

	// Finish -> success, slot released.
	ti.State = model.TaskSuccess
	ti.FinishedAt = &now
	if err := s.UpdateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}
	n, _ = s.CountRunningInPool(ctx, model.DefaultPoolName)
	if n != 0 {
		t.Errorf("pool occupancy after success = %d, want 0", n)
	}

	// Query helpers.
	byRun, err := s.ListTaskInstances(ctx, "etl__r1")
	if err != nil || len(byRun) != 1 {
		t.Fatalf("ListTaskInstances: n=%d err=%v", len(byRun), err)
	}
	byState, err := s.ListTaskInstancesByState(ctx, model.TaskSuccess)
	if err != nil || len(byState) != 1 {
		t.Fatalf("ListTaskInstancesByState: n=%d err=%v", len(byState), err)
	}
}

func TestForeignKeyEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// dag_run referencing a non-existent dag must fail (FK on), and must NOT be
	// mistaken for an already-exists error.
	run := &model.DagRun{RunID: "x", DagID: "nope", LogicalDate: time.Now().UTC(), State: model.RunQueued, TriggerType: model.TriggerManual}
	err := s.CreateDagRun(ctx, run)
	if err == nil {
		t.Fatal("expected FK violation")
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		t.Fatal("FK violation must not map to ErrAlreadyExists")
	}
}

func TestDagDependencies(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		mustDAG(t, s, id)
	}
	// c depends on a and b.
	if err := s.ReplaceDagDependencies(ctx, "c", []string{"a", "b"}); err != nil {
		t.Fatalf("ReplaceDagDependencies: %v", err)
	}
	ups, err := s.ListUpstreams(ctx, "c")
	if err != nil || len(ups) != 2 {
		t.Fatalf("ListUpstreams = %v err=%v, want [a b]", ups, err)
	}
	downs, err := s.ListDownstreams(ctx, "a")
	if err != nil || len(downs) != 1 || downs[0] != "c" {
		t.Fatalf("ListDownstreams(a) = %v err=%v, want [c]", downs, err)
	}
	// Replace is authoritative: now c depends only on b.
	if err := s.ReplaceDagDependencies(ctx, "c", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	ups, _ = s.ListUpstreams(ctx, "c")
	if len(ups) != 1 || ups[0] != "b" {
		t.Errorf("after replace, upstreams = %v, want [b]", ups)
	}
	if downs, _ := s.ListDownstreams(ctx, "a"); len(downs) != 0 {
		t.Errorf("a should have no downstreams after replace, got %v", downs)
	}
}

func mustDAG(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.UpsertDAG(context.Background(), &model.DAG{
		DagID: id, MaxActiveRuns: 1, StartDate: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed dag %q: %v", id, err)
	}
}
