package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

func TestRenderCommand(t *testing.T) {
	vars := map[string]string{"logical_date": "2026-06-09", "run_id": "r1"}
	got := renderCommand("etl {{ logical_date }} id={{run_id}} keep={{ unknown }}", func(k string) (string, bool) { v, ok := vars[k]; return v, ok })
	want := "etl 2026-06-09 id=r1 keep={{ unknown }}"
	if got != want {
		t.Errorf("renderCommand = %q, want %q", got, want)
	}
}

func TestCommandTemplating(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "out")
	dag := &model.DAG{
		DagID: "tmpl", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "t", Command: "echo date={{ logical_date }} run={{ run_id }} > " + out, Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "tmpl", nil)
	if run := s.driveToTerminal(t, ctx, runID, 20); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	s.WaitInflight()
	if !strings.Contains(string(data), "run="+runID) {
		t.Errorf("run_id not substituted: %q", data)
	}
	if strings.Contains(string(data), "{{") {
		t.Errorf("placeholders not rendered: %q", data)
	}
}

func TestCatchupBackfills(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-5 * time.Second)
	dag := &model.DAG{
		DagID: "cu", Schedule: "@every 1s", StartDate: start, Catchup: true, MaxActiveRuns: 10,
		Tasks: []model.Task{{ID: "t", Command: "echo $CRONOVA_LOGICAL_DATETIME", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
	}

	runs, _ := s.store.ListDagRuns(ctx, "cu", 100) // DESC
	var sched []*model.DagRun
	for _, r := range runs {
		if r.TriggerType == model.TriggerSchedule {
			sched = append(sched, r)
		}
	}
	if len(sched) < 3 {
		t.Fatalf("expected >=3 backfilled runs, got %d", len(sched))
	}
	for _, r := range sched {
		if r.State != model.RunSuccess {
			t.Errorf("backfilled run %s = %s, want success", r.RunID, r.State)
		}
	}
	// Earliest logical_date must be near start_date — proving backfill from the
	// start, not from "now".
	earliest := sched[len(sched)-1].LogicalDate
	if earliest.After(start.Add(2 * time.Second)) {
		t.Errorf("earliest logical_date %v is not near start %v; catchup did not backfill", earliest, start)
	}
	// Logical dates are distinct and ~1s apart.
	seen := map[string]bool{}
	for _, r := range sched {
		k := r.LogicalDate.Format(time.RFC3339)
		if seen[k] {
			t.Errorf("duplicate logical_date %s", k)
		}
		seen[k] = true
	}
}

func TestNoCatchupSkipsBackfill(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-5 * time.Second)
	dag := &model.DAG{
		DagID: "nocu", Schedule: "@every 1s", StartDate: start, Catchup: false, MaxActiveRuns: 10,
		Tasks: []model.Task{{ID: "t", Command: "echo hi", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		s.tickOnce(ctx)
		s.WaitInflight()
	}
	runs, _ := s.store.ListDagRuns(ctx, "nocu", 100)
	for _, r := range runs {
		if r.TriggerType == model.TriggerSchedule && r.LogicalDate.Before(s.bootTime) {
			t.Errorf("non-catchup created backfilled run with logical_date %v before boot %v", r.LogicalDate, s.bootTime)
		}
	}
}
