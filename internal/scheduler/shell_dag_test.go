package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

// newTestSchedulerWithDir is like newTestScheduler but with a DAG directory, so
// CreateDAG persists YAML and LoadDAGs can read it back (the restart path).
func newTestSchedulerWithDir(t *testing.T, dagDir string) *Scheduler {
	t.Helper()
	st, err := sqlite.New(filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(st, executor.NewLocal(), Options{
		DagDir:       dagDir,
		LogDir:       filepath.Join(t.TempDir(), "logs"),
		Tick:         10 * time.Millisecond,
		PollInterval: 5 * time.Millisecond,
	})
}

// A 0-task "shell" DAG must not be manually triggerable: it would otherwise
// finalize instantly as a phantom success (no task instances => allTerminal).
func TestTriggerManualRejectsShellDAG(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{DagID: "shell", MaxActiveRuns: 1, StartDate: time.Now().UTC()} // no tasks
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	_, err := s.TriggerManual(ctx, "shell", nil)
	if !errors.Is(err, model.ErrNoTasks) {
		t.Fatalf("expected ErrNoTasks, got %v", err)
	}
	runs, err := s.store.ListDagRuns(ctx, "shell", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("expected no runs for a rejected trigger, got %d", len(runs))
	}
}

// A scheduled 0-task shell DAG must never create scheduled runs.
func TestScheduledShellDAGCreatesNoRuns(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{DagID: "shell", Schedule: "@every 1m", MaxActiveRuns: 1, StartDate: time.Now().UTC().Add(-time.Hour)}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	// Far-future "now" so any real schedule would be due.
	s.createDueRuns(ctx, time.Now().UTC().Add(time.Hour))
	runs, err := s.store.ListDagRuns(ctx, "shell", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("scheduled shell DAG should create no runs, got %d", len(runs))
	}
	if _, ok := s.NextSchedule(ctx, dag); ok {
		t.Error("NextSchedule should report no next fire for a shell DAG")
	}
}

// The real CreateDAG accepts a 0-task YAML, persists + registers it, and the
// shell remains non-triggerable. After re-creating with a task, it runs.
func TestCreateDAGAllowsShell(t *testing.T) {
	dir := t.TempDir()
	s := newTestSchedulerWithDir(t, dir)
	ctx := context.Background()
	id, err := s.CreateDAG(ctx, "dag_id: shell\n")
	if err != nil {
		t.Fatalf("CreateDAG shell: %v", err)
	}
	if id != "shell" {
		t.Fatalf("id = %q", id)
	}
	if _, err := os.Stat(filepath.Join(dir, "shell.yaml")); err != nil {
		t.Errorf("shell.yaml not persisted: %v", err)
	}
	if _, err := s.TriggerManual(ctx, "shell", nil); !errors.Is(err, model.ErrNoTasks) {
		t.Errorf("shell trigger = %v, want ErrNoTasks", err)
	}
}

// A 0-task YAML file on disk loads at boot without failing (restart path).
func TestLoadDAGsAllowsShellFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell.yaml"), []byte("dag_id: shell\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestSchedulerWithDir(t, dir)
	ctx := context.Background()
	if err := s.LoadDAGs(ctx); err != nil {
		t.Fatalf("LoadDAGs: %v", err)
	}
	// Registered (not "not found"), but non-triggerable.
	if _, err := s.TriggerManual(ctx, "shell", nil); !errors.Is(err, model.ErrNoTasks) {
		t.Errorf("after load, trigger = %v, want ErrNoTasks", err)
	}
}

// If a DAG's last task is deleted after a run is queued, the run must FAIL (no
// work to do) rather than finalize as a phantom success.
func TestEmptiedRunFailsNotPhantomSuccess(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "shrink", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "echo a", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "shrink", nil) // run queued while a task exists
	if err != nil {
		t.Fatal(err)
	}
	// Simulate "delete last task" landing before the run's first expansion.
	empty := &model.DAG{DagID: "shrink", MaxActiveRuns: 1, StartDate: dag.StartDate}
	if err := s.registerDAG(ctx, empty); err != nil {
		t.Fatal(err)
	}
	run := s.driveToTerminal(t, ctx, runID, 20)
	if run.State != model.RunFailed {
		t.Errorf("emptied run = %s, want failed (not phantom success)", run.State)
	}
}

// A 0-task shell declared as a downstream (trigger_after) must not get a run
// when its upstream succeeds.
func TestTriggerDownstreamsSkipsShellDownstream(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	up := &model.DAG{
		DagID: "up", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "echo a", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess}},
	}
	down := &model.DAG{DagID: "down", MaxActiveRuns: 1, StartDate: time.Now().UTC(), TriggerAfter: []string{"up"}} // shell
	if err := s.registerDAG(ctx, up); err != nil {
		t.Fatal(err)
	}
	if err := s.registerDAG(ctx, down); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "up", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := s.driveToTerminal(t, ctx, runID, 40)
	if run.State != model.RunSuccess {
		t.Fatalf("upstream = %s, want success", run.State)
	}
	runs, err := s.store.ListDagRuns(ctx, "down", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("shell downstream should get no run, got %d", len(runs))
	}
}

// Once a task is added, the same DAG becomes triggerable and runs to success.
func TestShellDAGBecomesRunnableAfterAddingTask(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "grown", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "a", Command: "echo a", Pool: model.DefaultPoolName, TriggerRule: model.RuleAllSuccess}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := s.TriggerManual(ctx, "grown", nil)
	if err != nil {
		t.Fatalf("trigger after adding a task should succeed, got %v", err)
	}
	run := s.driveToTerminal(t, ctx, runID, 40)
	if run.State != model.RunSuccess {
		t.Errorf("run = %s, want success", run.State)
	}
}
