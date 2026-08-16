package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// TestRunTransitionGuard: the store refuses lifecycle-corrupting run-state
// writes (plan1 W12) — a run can never move backward to queued, a success can
// never become cancelled/timed_out, while every legitimate path (start,
// finalize, cancel, retry reactivation, mark override) still works.
func TestRunTransitionGuard(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDAG(ctx, &model.DAG{DagID: "d", DefinitionYAML: "dag_id: d\ntasks: []\n", MaxActiveRuns: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mk := func(id string, s model.RunState) {
		t.Helper()
		if err := st.CreateDagRun(ctx, &model.DagRun{RunID: id, DagID: "d", LogicalDate: now.Add(time.Duration(len(id)) * time.Minute), State: model.RunQueued, TriggerType: model.TriggerManual}); err != nil {
			t.Fatal(err)
		}
		if s != model.RunQueued {
			if s != model.RunRunning {
				if err := st.UpdateDagRunState(ctx, id, model.RunRunning, &now, nil); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := st.UpdateDagRunState(ctx, id, s, &now, nil); err != nil {
					t.Fatal(err)
				}
				return
			}
			if s != model.RunRunning {
				if err := st.UpdateDagRunState(ctx, id, s, &now, &now); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	// Legal chain: queued → running → success → running (retry) → failed →
	// success (mark override).
	mk("legal", model.RunQueued)
	steps := []model.RunState{model.RunRunning, model.RunSuccess, model.RunRunning, model.RunFailed, model.RunSuccess}
	for _, s := range steps {
		if err := st.UpdateDagRunState(ctx, "legal", s, &now, &now); err != nil {
			t.Fatalf("legal step → %s refused: %v", s, err)
		}
	}

	// Illegal: nothing may return to queued.
	if err := st.UpdateDagRunState(ctx, "legal", model.RunQueued, nil, nil); !errors.Is(err, model.ErrIllegalRunTransition) {
		t.Fatalf("→ queued accepted (err=%v)", err)
	}
	// Illegal: success → cancelled.
	if err := st.UpdateDagRunState(ctx, "legal", model.RunCancelled, &now, &now); !errors.Is(err, model.ErrIllegalRunTransition) {
		t.Fatalf("success → cancelled accepted (err=%v)", err)
	}
	// Illegal: success → timed_out.
	if err := st.UpdateDagRunState(ctx, "legal", model.RunTimedOut, &now, &now); !errors.Is(err, model.ErrIllegalRunTransition) {
		t.Fatalf("success → timed_out accepted (err=%v)", err)
	}
	// The illegal attempts left the state untouched.
	run, err := st.GetDagRun(ctx, "legal")
	if err != nil || run.State != model.RunSuccess {
		t.Fatalf("state after illegal attempts = %v (%v), want success", run, err)
	}
}
