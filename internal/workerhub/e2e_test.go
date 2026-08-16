package workerhub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/certs"
	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
	"github.com/zoyluo/cronova/internal/worker"
	workerv1 "github.com/zoyluo/cronova/proto/cronova/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// testCluster spins up a real hub (mTLS gRPC on loopback) plus one dial-in
// worker whose identity was issued by the test CA — the full production path
// minus the HTTP join endpoint (files are laid down directly).
type testCluster struct {
	hub    *Hub
	st     *sqlite.Store
	agent  *worker.Agent
	cancel context.CancelFunc
	dir    string
}

func startCluster(t *testing.T, workerID, group string, hubOpts Options) *testCluster {
	t.Helper()
	dir := t.TempDir()
	ca, _, err := certs.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.New(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"group": group}
	if err := st.UpsertWorker(context.Background(), &model.Worker{
		ID: workerID, Name: "e2e", Labels: labels, State: model.WorkerOffline,
	}); err != nil {
		t.Fatal(err)
	}

	// Hub listener with mTLS.
	serverCertPEM, serverKeyPEM, err := ca.ServerCert([]string{"127.0.0.1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	serverCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if hubOpts.Logger == nil {
		hubOpts.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	hub := New(st, hubOpts)
	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certs.CAPool(ca),
		MinVersion:   tls.VersionTLS12,
	})))
	workerv1.RegisterWorkerHubServer(grpcSrv, hub)
	// Plain TCP listener: grpc.Creds terminates TLS itself (and that is what
	// exposes the client certificate to peerWorkerID).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = grpcSrv.Serve(ln) }()
	t.Cleanup(grpcSrv.Stop)

	// Worker identity files, as Join would have written them.
	wdir := filepath.Join(dir, "wstate")
	if err := os.MkdirAll(wdir, 0o700); err != nil {
		t.Fatal(err)
	}
	csrPEM, keyPEM, err := certs.NewCSR()
	if err != nil {
		t.Fatal(err)
	}
	certPEM, err := ca.SignWorkerCSR(csrPEM, workerID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	writeF := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(wdir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeF("worker.key", keyPEM)
	writeF("worker.crt", certPEM)
	writeF("ca.crt", ca.CertPEM())
	idJSON, _ := json.Marshal(map[string]any{
		"worker_id": workerID, "name": "e2e", "labels": labels, "hub_addr": ln.Addr().String(),
	})
	writeF("worker.json", idJSON)

	agent, err := worker.New(wdir, "test", hubOpts.Logger)
	if err != nil {
		t.Fatal(err)
	}
	// Separate lifetimes: cancelling the worker (failover tests) must not
	// stop the hub's liveness sweep.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	hubCtx, hubCancel := context.WithCancel(context.Background())
	go func() { _ = agent.Run(workerCtx) }()
	go hub.Run(hubCtx)
	t.Cleanup(workerCancel)
	t.Cleanup(hubCancel)

	c := &testCluster{hub: hub, st: st, agent: agent, cancel: workerCancel, dir: dir}
	c.waitGroupUp(t, group, 5*time.Second)
	return c
}

func (c *testCluster) waitGroupUp(t *testing.T, group string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.hub.GroupAvailable(group) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("group %q never came up", group)
}

func waitPhase(t *testing.T, exec executor.Executor, ref string, want executor.Phase, timeout time.Duration) executor.Status {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := exec.Probe(context.Background(), ref)
		if err == nil && st.Phase == want {
			return st
		}
		time.Sleep(30 * time.Millisecond)
	}
	st, _ := exec.Probe(context.Background(), ref)
	t.Fatalf("ref %s never reached phase %v (now %+v)", ref, want, st)
	return executor.Status{}
}

// TestE2ERemoteTask: assign → execute on the worker → exit code, streamed log
// bytes on the scheduler side, and remote $CRONOVA_OUTPUT all arrive.
func TestE2ERemoteTask(t *testing.T) {
	c := startCluster(t, "wk_e2e1", "default", Options{})
	exec := c.hub.ExecutorForGroup("default")

	logPath := filepath.Join(c.dir, "logs", "run1", "t1.log")
	outPath := executor.OutputPath(logPath, 1)
	ref := "run1/t1/1"
	_, err := exec.Launch(context.Background(), executor.Spec{
		TaskRunID: ref,
		Command:   `echo hello-from-worker && echo '{"rows":"42"}' > "$CRONOVA_OUTPUT"`,
		Env:       map[string]string{"CRONOVA_OUTPUT": outPath},
		LogPath:   logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, exec, ref, executor.PhaseExited, 10*time.Second)
	if st.ExitCode != 0 {
		t.Fatalf("exit = %d, want 0", st.ExitCode)
	}
	// Log bytes must land in the scheduler-side file (streamed, not shared FS).
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(logPath)
		if strings.Contains(string(b), "hello-from-worker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log never streamed; got %q", b)
		}
		time.Sleep(30 * time.Millisecond)
	}
	// Remote XCom: the output file postmarked to the scheduler-side path.
	for {
		if b, err := os.ReadFile(outPath); err == nil {
			if !strings.Contains(string(b), `"rows":"42"`) {
				t.Fatalf("output = %q", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote output never arrived")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// TestE2ECancel: a cancel reaches the worker and kills the process group.
func TestE2ECancel(t *testing.T) {
	c := startCluster(t, "wk_e2e2", "default", Options{})
	exec := c.hub.ExecutorForGroup("default")
	ref := "run2/sleepy/1"
	logPath := filepath.Join(c.dir, "logs", "run2", "sleepy.log")
	if _, err := exec.Launch(context.Background(), executor.Spec{
		TaskRunID: ref, Command: "sleep 30", LogPath: logPath,
		Env: map[string]string{"CRONOVA_OUTPUT": executor.OutputPath(logPath, 1)},
	}); err != nil {
		t.Fatal(err)
	}
	// Give the assignment a moment to start remotely, then kill it.
	time.Sleep(400 * time.Millisecond)
	if err := exec.Cancel(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	st := waitPhase(t, exec, ref, executor.PhaseExited, 10*time.Second)
	if st.ExitCode == 0 {
		t.Fatalf("cancelled task reported exit 0")
	}
}

// TestE2EWorkerLostFailover: killing the worker mid-task flips the ref to
// UNKNOWN after the lost window, which is the scheduler's failover trigger.
func TestE2EWorkerLostFailover(t *testing.T) {
	c := startCluster(t, "wk_e2e3", "default", Options{
		Heartbeat: 100 * time.Millisecond,
		LostAfter: 300 * time.Millisecond,
		BootGrace: 300 * time.Millisecond,
	})
	exec := c.hub.ExecutorForGroup("default")
	ref := "run3/doomed/1"
	logPath := filepath.Join(c.dir, "logs", "run3", "doomed.log")
	if _, err := exec.Launch(context.Background(), executor.Spec{
		TaskRunID: ref, Command: "sleep 30", LogPath: logPath,
		Env: map[string]string{"CRONOVA_OUTPUT": executor.OutputPath(logPath, 1)},
	}); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, exec, ref, executor.PhaseRunning, 5*time.Second)
	c.cancel() // worker (and its heartbeats) die; its process keeps running but the fleet lost it
	st := waitPhase(t, exec, ref, executor.PhaseUnknown, 10*time.Second)
	_ = st
	// The store row reflects the loss for the console.
	deadline := time.Now().Add(5 * time.Second)
	for {
		w, err := c.st.GetWorker(context.Background(), "wk_e2e3")
		if err == nil && w.State == model.WorkerLost {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never marked lost (state=%v)", w)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
