package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
	"github.com/zoyluo/cronova/internal/store/postgres"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

// TestThroughputBench is the reproducible scheduler benchmark behind
// docs/BENCHMARKS.md. It is opt-in (skipped unless CRONOVA_BENCH=1) because it
// runs minutes-scale load, and it prints a machine-readable summary line.
//
//	CRONOVA_BENCH=1 go test ./internal/scheduler/ -run TestThroughputBench -v            # SQLite
//	CRONOVA_BENCH=1 CRONOVA_TEST_PG_DSN=postgres://... go test ... -run TestThroughputBench -v
//
// Knobs: CRONOVA_BENCH_DAGS (default 20), CRONOVA_BENCH_RUNS (runs per DAG,
// default 25), CRONOVA_BENCH_TASKS (chain length per run, default 3).
func TestThroughputBench(t *testing.T) {
	if os.Getenv("CRONOVA_BENCH") != "1" {
		t.Skip("set CRONOVA_BENCH=1 to run the throughput benchmark")
	}
	dags := envInt("CRONOVA_BENCH_DAGS", 20)
	runsPer := envInt("CRONOVA_BENCH_RUNS", 25)
	tasks := envInt("CRONOVA_BENCH_TASKS", 3)

	var st store.Store
	backend := "sqlite"
	if dsn := os.Getenv("CRONOVA_TEST_PG_DSN"); dsn != "" {
		backend = "postgres"
		pg, err := postgres.New(dsn)
		if err != nil {
			t.Fatalf("postgres: %v", err)
		}
		defer pg.Close()
		if err := pg.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		st = pg
	} else {
		sq, err := sqlite.New(filepath.Join(t.TempDir(), "bench.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer sq.Close()
		if err := sq.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		st = sq
	}

	s := New(st, executor.NewLocal(), Options{
		LogDir:              filepath.Join(t.TempDir(), "logs"),
		Tick:                10 * time.Millisecond,
		PollInterval:        5 * time.Millisecond,
		MaxActiveRunsGlobal: 200,
		MaxConcurrentTasks:  64,
		MaxQueuedRunsGlobal: dags*runsPer + 100,
	})
	ctx := context.Background()

	// Chain of `tasks` trivial shell tasks per DAG. The prefix is unique per
	// invocation so a persistent backend (PG) can host repeated runs without
	// colliding with previous bench data.
	prefix := fmt.Sprintf("bench%d", time.Now().Unix()%1_000_000)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < dags; i++ {
		var ts []model.Task
		for j := 0; j < tasks; j++ {
			task := model.Task{ID: fmt.Sprintf("t%d", j), Command: "true", Pool: model.DefaultPoolName}
			if j > 0 {
				task.Deps = []string{fmt.Sprintf("t%d", j-1)}
			}
			ts = append(ts, task)
		}
		d := &model.DAG{
			DagID: fmt.Sprintf("%s_%03d", prefix, i), MaxActiveRuns: 50,
			StartDate: base, Tasks: ts,
		}
		if err := s.registerDAG(ctx, d); err != nil {
			t.Fatal(err)
		}
		for r := 0; r < runsPer; r++ {
			run := &model.DagRun{
				RunID: fmt.Sprintf("%s_%03d__r%03d", prefix, i, r), DagID: d.DagID,
				LogicalDate: base.Add(time.Duration(r) * time.Hour),
				State:       model.RunQueued, TriggerType: model.TriggerManual,
			}
			snapshotRun(run, d)
			if err := st.CreateDagRun(ctx, run); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Drain progress is measured against a pre-run baseline so a persistent
	// backend carrying older bench data cannot fake completion.
	baseline, err := st.CountRunsByState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	total := dags * runsPer
	start := time.Now()
	deadline := start.Add(10 * time.Minute)
	for {
		s.tickOnce(ctx)
		s.WaitInflight()
		byState, err := st.CountRunsByState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		done := byState[model.RunSuccess] - baseline[model.RunSuccess] +
			byState[model.RunFailed] - baseline[model.RunFailed]
		if done >= total {
			if failed := byState[model.RunFailed] - baseline[model.RunFailed]; failed > 0 {
				t.Fatalf("%d runs failed", failed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("benchmark stalled: %d/%d done", done, total)
		}
	}
	wall := time.Since(start)

	// Run-duration distribution (created→finished is not stored; started→
	// finished is the dispatch-to-done path the scheduler controls).
	var durs []time.Duration
	for i := 0; i < dags; i++ {
		runs, err := st.ListDagRuns(ctx, fmt.Sprintf("%s_%03d", prefix, i), runsPer+1)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range runs {
			if r.StartedAt != nil && r.FinishedAt != nil {
				durs = append(durs, r.FinishedAt.Sub(*r.StartedAt))
			}
		}
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	pct := func(p float64) time.Duration {
		if len(durs) == 0 {
			return 0
		}
		i := int(p * float64(len(durs)-1))
		return durs[i]
	}

	t.Logf("BENCH backend=%s dags=%d runs=%d tasks_per_run=%d wall=%s runs_per_sec=%.1f tasks_per_sec=%.1f run_p50=%s run_p99=%s",
		backend, dags, total, tasks, wall.Round(time.Millisecond),
		float64(total)/wall.Seconds(), float64(total*tasks)/wall.Seconds(),
		pct(0.50).Round(time.Millisecond), pct(0.99).Round(time.Millisecond))
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
