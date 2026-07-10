package executor

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// shortSocketDir returns a temp dir under /tmp. Unix socket paths have a ~104
// byte limit on macOS, and t.TempDir() embeds the (long) test name, so we use a
// short path instead.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cnv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startTestServer runs a gRPC executor on a temp unix socket, returning the
// dial target and a stop func.
func startTestServer(t *testing.T) (string, func()) {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "e.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterExecutorServer(srv, NewGRPCServer(NewRunner()))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	return "unix://" + sock, srv.Stop
}

func TestGRPCExecutorSuccess(t *testing.T) {
	target, stop := startTestServer(t)
	defer stop()

	c, err := Dial(target)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	ref, err := c.Launch(context.Background(), Spec{
		TaskRunID: "run1/task1",
		Command:   "echo over-grpc",
		LogPath:   filepath.Join(t.TempDir(), "g.log"),
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if ref != "run1/task1" {
		t.Errorf("ref = %q, want run1/task1", ref)
	}
	if st := waitExited(t, c, ref, 3*time.Second); st.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", st.ExitCode)
	}
}

func TestGRPCExecutorProbeUnknownAndCancel(t *testing.T) {
	target, stop := startTestServer(t)
	defer stop()
	c, err := Dial(target)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()

	if st, _ := c.Probe(ctx, "nope"); st.Phase != PhaseUnknown {
		t.Errorf("unknown ref phase = %v, want PhaseUnknown", st.Phase)
	}

	ref, err := c.Launch(ctx, Spec{TaskRunID: "r/c", Command: "sleep 30", LogPath: filepath.Join(t.TempDir(), "c.log")})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := c.Cancel(ctx, ref); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if st := waitExited(t, c, ref, 3*time.Second); st.ExitCode == 0 {
		t.Errorf("cancelled task exit = 0, want non-zero")
	}
}

func TestDialRejectsNonUnixTargets(t *testing.T) {
	for _, target := range []string{"localhost:9091", "dns:///executor.internal:9091", "unix://relative.sock", "unix:///tmp/e.sock?x=1"} {
		if c, err := Dial(target); err == nil {
			_ = c.Close()
			t.Errorf("Dial(%q) accepted a target outside the Unix-socket trust boundary", target)
		}
	}
}
