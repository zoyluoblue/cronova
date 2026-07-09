package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitExited polls until the ref reports PhaseExited or the deadline passes.
func waitExited(t *testing.T, e Executor, ref string, within time.Duration) Status {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		st, err := e.Probe(context.Background(), ref)
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if st.Phase == PhaseExited {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ref %s did not exit within %s", ref, within)
	return Status{}
}

func TestLocalSuccess(t *testing.T) {
	e := NewLocal()
	logPath := filepath.Join(t.TempDir(), "ok.log")
	ref, err := e.Launch(context.Background(), Spec{TaskRunID: "r/t", Command: "echo hello && echo world", LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	st := waitExited(t, e, ref, 3*time.Second)
	if st.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", st.ExitCode)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "hello") || !strings.Contains(string(data), "world") {
		t.Errorf("log missing output:\n%s", data)
	}
}

func TestLocalDir(t *testing.T) {
	e := NewLocal()
	workdir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "dir.log")
	// pwd resolves symlinks (/var -> /private/var on macOS); compare via -ef instead.
	ref, err := e.Launch(context.Background(), Spec{
		TaskRunID: "r/t",
		Command:   `[ "$PWD" -ef "` + workdir + `" ] && echo CWD_OK`,
		Dir:       workdir,
		LogPath:   logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := waitExited(t, e, ref, 3*time.Second); st.ExitCode != 0 {
		data, _ := os.ReadFile(logPath)
		t.Fatalf("exit=%d, cmd did not run in Dir; log:\n%s", st.ExitCode, data)
	}
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "CWD_OK") {
		t.Errorf("command did not run with cwd=%s; log:\n%s", workdir, data)
	}
}

// TestLocalMissingDir: a set working dir that doesn't exist (a project-attached
// task on an executor that doesn't share the scheduler's filesystem) fails with a
// clear, shared-filesystem message rather than a cryptic chdir error.
func TestLocalMissingDir(t *testing.T) {
	e := NewLocal()
	_, err := e.Launch(context.Background(), Spec{
		TaskRunID: "r/t",
		Command:   "echo hi",
		Dir:       filepath.Join(t.TempDir(), "does-not-exist"),
		LogPath:   filepath.Join(t.TempDir(), "m.log"),
	})
	if err == nil {
		t.Fatal("expected an error for a missing working directory")
	}
	if !strings.Contains(err.Error(), "filesystem") {
		t.Errorf("error should explain the shared-filesystem requirement, got: %v", err)
	}
}

func TestLocalFailure(t *testing.T) {
	e := NewLocal()
	ref, err := e.Launch(context.Background(), Spec{TaskRunID: "r/t", Command: "exit 3", LogPath: filepath.Join(t.TempDir(), "f.log")})
	if err != nil {
		t.Fatal(err)
	}
	if st := waitExited(t, e, ref, 3*time.Second); st.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", st.ExitCode)
	}
}

func TestLocalTimeout(t *testing.T) {
	e := NewLocal()
	start := time.Now()
	ref, err := e.Launch(context.Background(), Spec{TaskRunID: "r/t", Command: "sleep 10", Timeout: 200 * time.Millisecond, LogPath: filepath.Join(t.TempDir(), "to.log")})
	if err != nil {
		t.Fatal(err)
	}
	st := waitExited(t, e, ref, 3*time.Second)
	if time.Since(start) > 3*time.Second {
		t.Fatal("timeout did not kill promptly")
	}
	if st.ExitCode != TimeoutExitCode {
		t.Errorf("exit code = %d, want %d", st.ExitCode, TimeoutExitCode)
	}
}

func TestLocalIdempotentLaunch(t *testing.T) {
	e := NewLocal()
	logPath := filepath.Join(t.TempDir(), "idem.log")
	spec := Spec{TaskRunID: "same", Command: "echo once >> " + filepath.Join(t.TempDir(), "count"), LogPath: logPath}
	r1, _ := e.Launch(context.Background(), spec)
	r2, err := e.Launch(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if r1 != r2 || r1 != "same" {
		t.Errorf("idempotent launch refs differ: %q %q", r1, r2)
	}
	waitExited(t, e, r1, 3*time.Second)
}

func TestRunnerRedactsSecretsInLog(t *testing.T) {
	r := NewRunner()
	logPath := filepath.Join(t.TempDir(), "red.log")
	secret := "S3cr3tP@ss"
	// The command embeds the secret (echoed on the "$ " line) AND prints it with NO
	// trailing newline (so it flushes only on Close) — both must be masked.
	ref, err := r.Launch(Spec{
		TaskRunID: "r/red/1",
		Command:   "printf 'pw=%s' " + secret,
		LogPath:   logPath,
		Redact:    []string{secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for r.Probe(ref).Phase != PhaseExited {
		if time.Now().After(deadline) {
			t.Fatal("task never exited")
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if strings.Contains(log, secret) {
		t.Fatalf("secret leaked into task log:\n%s", log)
	}
	if !strings.Contains(log, "****") {
		t.Fatalf("expected masked marker in log:\n%s", log)
	}
}

func TestRedactWriterCappedBuffer(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "capped.log")
	f, err := os.Create(tmp)
	if err != nil {
		t.Fatal(err)
	}
	secret := "TOPSECRET"
	rw := newLogSink(f, []string{secret}).(*redactWriter)

	// A newline-less stream larger than the cap forces a mid-stream flush; secrets
	// on BOTH sides of the flush (well clear of the cut) must still be masked, and
	// no bytes are lost.
	rw.Write([]byte(secret))                                 // before the forced flush
	rw.Write([]byte(strings.Repeat("x", redactBufCap+4096))) // trips the cap
	rw.Write([]byte(secret))                                 // after the forced flush
	rw.Write([]byte("\n"))
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("secret leaked past the capped buffer")
	}
	if !strings.Contains(string(data), "****") {
		t.Fatal("expected mask marker in capped output")
	}
	// buffer never exceeded cap + one write + overlap — a rough bound check that the
	// stream was actually flushed in pieces rather than all held in memory.
	if len(data) < redactBufCap {
		t.Fatalf("expected the large stream to be written out, got %d bytes", len(data))
	}
}

// TestRedactWriterStraddleNoLeak drives a secret across the cap flush boundary so
// a naive cut would land inside it and emit its prefix in cleartext (the value
// then reassembles in the log). The flush must never split a complete secret —
// including the hard case where a secret's tail byte is also the start of ANOTHER
// secret (or the secret's own border), which fooled an earlier prefix-only cut.
func TestRedactWriterStraddleNoLeak(t *testing.T) {
	cases := []struct {
		name    string
		secrets []string
		leak    string // the value that must never appear whole in the log
		payload string // written right after the filler, in one write
	}{
		{"single", []string{"PASSWORD12"}, "PASSWORD12", "PASSWORD12yyyyyy"},
		{"cross-overlap", []string{"AB", "BXY"}, "AB", "ABqqqqqq"},        // "AB" ends in "B", a prefix of "BXY"
		{"self-border", []string{"aba"}, "aba", "abaqqqqqq"},              // "aba" border "a"
		{"nested", []string{"pass", "password"}, "password", "password!"}, // one secret prefixes another
	}
	for _, c := range cases {
		for fill := redactBufCap - len(c.payload); fill <= redactBufCap+1; fill++ {
			tmp := filepath.Join(t.TempDir(), "straddle.log")
			f, err := os.Create(tmp)
			if err != nil {
				t.Fatal(err)
			}
			rw := newLogSink(f, c.secrets).(*redactWriter)
			rw.Write([]byte(strings.Repeat("x", fill)))
			rw.Write([]byte(c.payload))
			rw.Write([]byte("\n"))
			if err := rw.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(tmp)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), c.leak) {
				t.Fatalf("%s fill=%d: secret %q leaked whole across the cap flush", c.name, fill, c.leak)
			}
		}
	}
}

func TestRunnerSweepFinished(t *testing.T) {
	r := NewRunner()
	dir := t.TempDir()

	done, err := r.Launch(Spec{TaskRunID: "r/done/1", Command: "true", LogPath: filepath.Join(dir, "d.log")})
	if err != nil {
		t.Fatal(err)
	}
	live, err := r.Launch(Spec{TaskRunID: "r/live/1", Command: "sleep 30", LogPath: filepath.Join(dir, "l.log")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Cancel(live) })

	// wait for the short task to finish
	deadline := time.Now().Add(3 * time.Second)
	for r.Probe(done).Phase != PhaseExited {
		if time.Now().After(deadline) {
			t.Fatal("done task never exited")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Age the finished entry past the TTL and sweep: it evicts, the running one stays.
	r.mu.Lock()
	r.tasks[done].finishedAt = time.Now().Add(-2 * finishedTaskTTL)
	r.sweepFinishedLocked(time.Now())
	_, doneKept := r.tasks[done]
	_, liveKept := r.tasks[live]
	r.mu.Unlock()

	if doneKept {
		t.Error("aged finished task should be evicted")
	}
	if !liveKept {
		t.Error("still-running task must never be swept")
	}
	// A re-Probe of the evicted ref reports Unknown (registry no longer holds it).
	if p := r.Probe(done).Phase; p != PhaseUnknown {
		t.Errorf("evicted ref phase = %v, want PhaseUnknown", p)
	}
}

func TestLocalProbeUnknown(t *testing.T) {
	e := NewLocal()
	st, err := e.Probe(context.Background(), "never-launched")
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseUnknown {
		t.Errorf("phase = %v, want PhaseUnknown", st.Phase)
	}
}

func TestLocalCancel(t *testing.T) {
	e := NewLocal()
	ref, err := e.Launch(context.Background(), Spec{TaskRunID: "r/c", Command: "sleep 30", LogPath: filepath.Join(t.TempDir(), "c.log")})
	if err != nil {
		t.Fatal(err)
	}
	// Give it a moment to be running, then cancel.
	time.Sleep(50 * time.Millisecond)
	if err := e.Cancel(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	st := waitExited(t, e, ref, 3*time.Second)
	if st.ExitCode == 0 {
		t.Errorf("cancelled task should not exit 0, got %d", st.ExitCode)
	}
}

func TestLocalEnvInjection(t *testing.T) {
	e := NewLocal()
	logPath := filepath.Join(t.TempDir(), "env.log")
	ref, _ := e.Launch(context.Background(), Spec{
		TaskRunID: "r/e",
		Command:   "echo date=$CRONOVA_LOGICAL_DATE",
		Env:       map[string]string{"CRONOVA_LOGICAL_DATE": "2026-06-09"},
		LogPath:   logPath,
	})
	waitExited(t, e, ref, 3*time.Second)
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "date=2026-06-09") {
		t.Errorf("env not injected:\n%s", data)
	}
}
