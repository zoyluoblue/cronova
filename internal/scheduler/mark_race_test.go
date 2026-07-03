package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// TestConcurrentMarkVsFinalizeNoWedge hammers MarkTask/RetryRun against the LIVE
// tick loop to catch the finalize-vs-mark wedge (a logical bug -race can't see):
// a run left terminal while a task it "released" sits non-terminal, never driven
// again. finalizeMu must make every run reach a consistent terminal state.
func TestConcurrentMarkVsFinalizeNoWedge(t *testing.T) {
	s := newTestScheduler(t)
	ctx, cancel := context.WithCancel(context.Background())
	dag := &model.DAG{
		DagID: "cw", MaxActiveRuns: 100, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			{ID: "a", Command: "exit 1", Pool: model.DefaultPoolName}, // fails → b upstream_failed
			{ID: "b", Command: "echo b", Deps: []string{"a"}, Pool: model.DefaultPoolName},
		},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Run(ctx) }() // real tick loop (10ms in tests)

	const N = 24
	runIDs := make([]string, 0, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		runID, err := s.TriggerManual(ctx, "cw", nil)
		if err != nil {
			t.Fatal(err)
		}
		runIDs = append(runIDs, runID)
		wg.Add(1)
		// race the tick's finalize: retry the mark until it lands (it may briefly hit
		// a run mid-transition), which is exactly the window the wedge lived in.
		go func(id string, kind int) {
			defer wg.Done()
			for k := 0; k < 400; k++ {
				var err error
				if kind%2 == 0 {
					err = s.MarkTask(ctx, id, "a", model.TaskSuccess)
				} else {
					err = s.RetryTask(ctx, id, "a")
				}
				if err == nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}(runID, i)
	}
	wg.Wait()

	// let the loop drain, then stop it and wait for in-flight tasks.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if allSettled(t, s, ctx, runIDs) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	s.WaitInflight()

	// INVARIANT: no run is terminal while any of its tasks is still non-terminal.
	// (fresh context — the loop's ctx is now cancelled.)
	bg := context.Background()
	for _, id := range runIDs {
		run, err := s.store.GetDagRun(bg, id)
		if err != nil {
			t.Fatalf("GetDagRun %s: %v", id, err)
		}
		if !run.State.IsTerminal() {
			t.Fatalf("run %s never settled (state=%s) — likely a wedge", id, run.State)
		}
		for tid, st := range s.tiStates(t, bg, id) {
			if !st.IsTerminal() {
				t.Fatalf("WEDGE: run %s is %s but task %s is %s (non-terminal, never re-driven)", id, run.State, tid, st)
			}
		}
	}
}

func allSettled(t *testing.T, s *Scheduler, ctx context.Context, runIDs []string) bool {
	t.Helper()
	for _, id := range runIDs {
		run, err := s.store.GetDagRun(ctx, id)
		if err != nil || !run.State.IsTerminal() {
			return false
		}
		for _, st := range s.tiStates(t, ctx, id) {
			if !st.IsTerminal() {
				return false
			}
		}
	}
	return true
}
