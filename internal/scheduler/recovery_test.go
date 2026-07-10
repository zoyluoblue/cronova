package scheduler

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// startExecutorServer runs a gRPC executor on a temp socket. It survives the
// scheduler instances created below, mimicking the long-lived executor process.
func startExecutorServer(t *testing.T) (string, func()) {
	t.Helper()
	// Unix socket paths are length-limited (~104 bytes on macOS); t.TempDir()
	// embeds the long test name, so use a short /tmp path.
	dir, err := os.MkdirTemp("/tmp", "cnv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "exec.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	pb.RegisterExecutorServer(srv, executor.NewGRPCServer(executor.NewRunner()))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	return "unix://" + sock, srv.Stop
}

func waitTaskState(t *testing.T, st *sqlite.Store, runID, taskID string, want model.TaskState, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		tis, err := st.ListTaskInstances(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		for _, ti := range tis {
			if ti.TaskID == taskID && ti.State == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s within %s", taskID, want, within)
}

// TestCrashRecoveryReattaches is the M2 acceptance test: kill the scheduler
// while a task runs, restart it, and the task must NOT be lost or re-run.
func TestCrashRecoveryReattaches(t *testing.T) {
	target, stopExec := startExecutorServer(t)
	defer stopExec()

	dbPath := filepath.Join(t.TempDir(), "rec.db")
	logDir := filepath.Join(t.TempDir(), "logs")
	marker := filepath.Join(t.TempDir(), "marker")

	dag := &model.DAG{
		DagID: "rec", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{
			// Record one run, then sleep so the task is still alive at crash time.
			{ID: "t", Command: "echo ran >> " + marker + " && sleep 2", Pool: model.DefaultPoolName},
		},
	}

	open := func(ctx context.Context) (*Scheduler, *sqlite.Store, *executor.GRPCClient) {
		st, err := sqlite.New(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		client, err := executor.Dial(target)
		if err != nil {
			t.Fatal(err)
		}
		s := New(st, client, Options{LogDir: logDir, Tick: 10 * time.Millisecond, PollInterval: 10 * time.Millisecond})
		return s, st, client
	}

	// --- Scheduler A: launch the task, then crash before it finishes. ---
	ctxA, cancelA := context.WithCancel(context.Background())
	a, stA, clA := open(ctxA)
	if err := a.registerDAG(ctxA, dag); err != nil {
		t.Fatal(err)
	}
	runID, err := a.TriggerManual(ctxA, "rec", nil)
	if err != nil {
		t.Fatal(err)
	}
	a.tickOnce(ctxA) // expand + dispatch + launch
	waitTaskState(t, stA, runID, "t", model.TaskRunning, 2*time.Second)

	cancelA()        // simulate crash: poll goroutine returns without finalizing
	a.WaitInflight() // its goroutine unwinds; task keeps running on the executor
	clA.Close()
	stA.Close()

	// --- Scheduler B: recover and finish. ---
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	b, stB, clB := open(ctxB)
	defer clB.Close()
	defer stB.Close()
	if err := b.registerDAG(ctxB, dag); err != nil {
		t.Fatal(err)
	}
	if err := b.Recover(ctxB); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	run := b.driveToTerminal(t, ctxB, runID, 100)
	if run.State != model.RunSuccess {
		t.Fatalf("recovered run state = %s, want success", run.State)
	}
	if states := b.tiStates(t, ctxB, runID); states["t"] != model.TaskSuccess {
		t.Errorf("task t = %s, want success", states["t"])
	}

	// Idempotency: the task ran exactly once across crash + recovery.
	data, _ := os.ReadFile(marker)
	if n := strings.Count(string(data), "ran"); n != 1 {
		t.Errorf("task ran %d times, want exactly 1 (recovery must not re-run)", n)
	}
}
