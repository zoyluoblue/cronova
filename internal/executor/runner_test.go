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
