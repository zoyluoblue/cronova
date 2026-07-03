package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// captureHook returns an httptest server that records every POST body, plus a
// snapshot accessor. Used to assert webhook delivery deterministically.
func captureHook(t *testing.T) (url string, bodies func() [][]byte) {
	t.Helper()
	var mu sync.Mutex
	var got [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		out := make([][]byte, len(got))
		copy(out, got)
		return out
	}
}

// TestNotifyFiresOnFailure: a DAG with notify_on:[failure] POSTs an honest
// payload — naming the failed task — when its run fails.
func TestNotifyFiresOnFailure(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "notif", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyURL: url, NotifyOn: []string{"failure"},
		Tasks: []model.Task{{ID: "boom", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "notif", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	s.WaitInflight() // the notify goroutine is tracked by s.inflight

	got := bodies()
	if len(got) != 1 {
		t.Fatalf("got %d webhook posts, want 1", len(got))
	}
	var p notifyPayload
	if err := json.Unmarshal(got[0], &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.State != "failed" {
		t.Errorf("state = %q, want failed", p.State)
	}
	if p.DagID != "notif" || p.RunID != runID {
		t.Errorf("ids = %s/%s, want notif/%s", p.DagID, p.RunID, runID)
	}
	if len(p.FailedTasks) != 1 || p.FailedTasks[0] != "boom" {
		t.Errorf("failed_tasks = %v, want [boom]", p.FailedTasks)
	}
	if p.Text == "" {
		t.Error("text summary must be non-empty (Slack/Feishu render it)")
	}
}

// TestNotifyFiresOnSuccess: notify_on:[success] fires on a clean run, with no
// failed_tasks in the payload.
func TestNotifyFiresOnSuccess(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "notok", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyURL: url, NotifyOn: []string{"success"},
		Tasks: []model.Task{{ID: "ok", Command: "echo hi", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "notok", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	s.WaitInflight()

	got := bodies()
	if len(got) != 1 {
		t.Fatalf("got %d webhook posts, want 1", len(got))
	}
	var p notifyPayload
	if err := json.Unmarshal(got[0], &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if p.State != "success" {
		t.Errorf("state = %q, want success", p.State)
	}
	if len(p.FailedTasks) != 0 {
		t.Errorf("failed_tasks = %v, want empty", p.FailedTasks)
	}
}

// TestNotifySkippedOnStateMismatch: a success-only webhook must NOT fire when the
// run fails (and vice versa is covered by the two firing tests).
func TestNotifySkippedOnStateMismatch(t *testing.T) {
	url, bodies := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "nofire", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyURL: url, NotifyOn: []string{"success"}, // but the run will FAIL
		Tasks: []model.Task{{ID: "boom", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "nofire", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	s.WaitInflight()

	if n := len(bodies()); n != 0 {
		t.Fatalf("webhook fired %d times, want 0 (success-only on a failed run)", n)
	}
}

// TestNotifyClientBlocksPrivateTarget: the SSRF guard refuses a loopback/private
// target at dial time, and lifting the guard restores delivery.
func TestNotifyClientBlocksPrivateTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()

	guarded := newNotifyClient(false) // production default
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	if _, err := guarded.Do(req); err == nil {
		t.Fatal("SSRF guard should refuse a 127.0.0.1 target, got nil error")
	}

	open := newNotifyClient(true) // test opt-in
	req2, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	resp, err := open.Do(req2)
	if err != nil {
		t.Fatalf("guard-off delivery failed: %v", err)
	}
	resp.Body.Close()
}

// TestNotifyClientDoesNotFollowRedirect: a 3xx is returned as-is, never chased —
// so a public URL can't 302-pivot the scheduler into an internal service.
func TestNotifyClientDoesNotFollowRedirect(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true; w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer front.Close()

	c := newNotifyClient(true) // guard off so the loopback front is reachable
	req, _ := http.NewRequest(http.MethodPost, front.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect must not be followed)", resp.StatusCode)
	}
	if followed {
		t.Fatal("client followed the redirect — SSRF pivot is possible")
	}
}

// TestNotifyTargetBlocked exhaustively checks the SSRF classifier: every
// non-public range (incl. NAT64-embedded metadata, CGNAT, broad multicast) is
// refused; ordinary public addresses pass.
func TestNotifyTargetBlocked(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", // loopback
		"10.0.0.5", "172.16.0.1", "192.168.1.1", "fd00::1", // private / ULA
		"169.254.169.254", "::ffff:169.254.169.254", "fe80::1", // link-local incl. metadata + mapped
		"0.0.0.0", "::", // unspecified
		"100.64.0.1", "100.127.255.255", // RFC6598 CGNAT
		"239.255.255.255", "ff05::1", "ff0e::1", // multicast (admin/site/global)
		"64:ff9b::a9fe:a9fe", "64:ff9b::7f00:1", // NAT64 embedding 169.254.169.254 / 127.0.0.1
	}
	for _, s := range blocked {
		if !notifyTargetBlocked(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"} {
		if notifyTargetBlocked(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if !notifyTargetBlocked(nil) {
		t.Error("nil ip should be blocked")
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// TestNotifyLogDoesNotLeakURLSecret: a delivery error must not write the full
// webhook URL (whose path is the Slack/Feishu secret) to the log — only host.
func TestNotifyLogDoesNotLeakURLSecret(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&lockedWriter{&mu, &buf}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := &Scheduler{log: logger, notifyClient: newNotifyClient(true)}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // port is now closed → connection refused → error path

	const secret = "SUPERSECRET_DEADBEEF"
	d := &model.DAG{DagID: "leak", NotifyURL: "http://" + addr + "/services/T/B/" + secret, NotifyOn: []string{"failure"}}
	run := &model.DagRun{RunID: "r1", DagID: "leak", LogicalDate: time.Now().UTC()}
	s.notifyRun(d, run, model.RunFailed, time.Now().UTC(), nil)
	s.WaitInflight()

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if strings.Contains(out, secret) {
		t.Fatalf("log leaked the URL secret path:\n%s", out)
	}
	host, _, _ := net.SplitHostPort(addr)
	if !strings.Contains(out, host) {
		t.Fatalf("log should still name the host for diagnosis:\n%s", out)
	}
}

// TestNotifyNoopWhenUnconfigured: a DAG without a notify_url never posts.
func TestNotifyNoopWhenUnconfigured(t *testing.T) {
	s := newTestScheduler(t)
	ctx := context.Background()
	// no NotifyURL/NotifyOn — notifyRun must early-return without touching the net.
	dag := &model.DAG{
		DagID: "plain", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		Tasks: []model.Task{{ID: "ok", Command: "echo hi", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "plain", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunSuccess {
		t.Fatalf("run = %s, want success", run.State)
	}
	s.WaitInflight() // must return promptly — no inflight webhook to wait on
}
