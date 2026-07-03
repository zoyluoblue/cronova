package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// TestUpdateTaskInstanceGuarded verifies the optimistic CAS: a write applies only
// when the row still carries the expected ref AND is non-terminal.
func TestUpdateTaskInstanceGuarded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.UpsertDAG(ctx, &model.DAG{DagID: "d", MaxActiveRuns: 1, StartDate: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDagRun(ctx, &model.DagRun{RunID: "d__r", DagID: "d", LogicalDate: time.Now().UTC(), State: model.RunRunning, TriggerType: model.TriggerManual}); err != nil {
		t.Fatal(err)
	}
	ti := &model.TaskInstance{RunID: "d__r", TaskID: "t", State: model.TaskRunning, Pool: "default", ExecutorRef: "d__r/t/1"}
	if err := s.CreateTaskInstance(ctx, ti); err != nil {
		t.Fatal(err)
	}

	// matching ref + non-terminal → applies
	ti.State = model.TaskSuccess
	applied, err := s.UpdateTaskInstanceGuarded(ctx, ti, "d__r/t/1")
	if err != nil || !applied {
		t.Fatalf("guarded write should apply: applied=%v err=%v", applied, err)
	}

	// reset to running for the next cases
	ti.State = model.TaskRunning
	_ = s.UpdateTaskInstance(ctx, ti)

	// now mark it cancelled (simulating CancelRun), then a stale finalize must NOT apply
	ti.State = model.TaskCancelled
	_ = s.UpdateTaskInstance(ctx, ti)
	stale := &model.TaskInstance{ID: ti.ID, RunID: "d__r", TaskID: "t", State: model.TaskSuccess, Pool: "default", ExecutorRef: "d__r/t/1"}
	applied, err = s.UpdateTaskInstanceGuarded(ctx, stale, "d__r/t/1")
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("guarded write clobbered a cancelled (terminal) row")
	}
	got, _ := s.GetTaskInstance(ctx, ti.ID)
	if got.State != model.TaskCancelled {
		t.Fatalf("cancelled row was overwritten: %s", got.State)
	}

	// mismatched ref (simulating a retry that cleared/rewrote the ref) → skip
	back := &model.TaskInstance{RunID: "d__r", TaskID: "t2", State: model.TaskRunning, Pool: "default", ExecutorRef: "d__r/t2/2"}
	_ = s.CreateTaskInstance(ctx, back)
	back.State = model.TaskSuccess
	applied, _ = s.UpdateTaskInstanceGuarded(ctx, back, "d__r/t2/1") // stale ref
	if applied {
		t.Fatal("guarded write applied despite a ref mismatch")
	}
}
