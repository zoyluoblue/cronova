package executor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A restarted runner re-adopts a still-running task and, once it exits, reports
// the REAL exit code from the sidecar file — not PhaseUnknown, not a fake failure.
func TestStateRecoveryAdoptsRunningTask(t *testing.T) {
	dir := t.TempDir()
	logs := t.TempDir()

	r1, err := NewRunnerWithState(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r1.Launch(Spec{
		TaskRunID: "run/task/1",
		Command:   "sleep 1; exit 7",
		LogPath:   filepath.Join(logs, "t.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := r1.Probe(ref); st.Phase != PhaseRunning {
		t.Fatalf("phase before restart = %v", st.Phase)
	}

	// "restart": a brand-new runner over the same state dir. Shutdown with
	// state persistence must NOT kill the running task.
	r1.Shutdown()
	r2, err := NewRunnerWithState(dir)
	if err != nil {
		t.Fatal(err)
	}
	adopted, _ := r2.RecoverState()
	if adopted != 1 {
		t.Fatalf("adopted = %d, want 1", adopted)
	}
	if st := r2.Probe(ref); st.Phase != PhaseRunning {
		t.Fatalf("adopted phase = %v, want running", st.Phase)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := r2.Probe(ref); st.Phase == PhaseExited {
			if st.ExitCode != 7 {
				t.Fatalf("adopted exit code = %d, want 7 (the real one)", st.ExitCode)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("adopted task never reported exit")
}

// A finished task's result survives the restart via the persisted state.
func TestStateRecoveryRestoresFinishedResult(t *testing.T) {
	dir := t.TempDir()
	logs := t.TempDir()
	r1, _ := NewRunnerWithState(dir)
	ref, err := r1.Launch(Spec{TaskRunID: "run/quick/1", Command: "exit 3", LogPath: filepath.Join(logs, "q.log")})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := r1.Probe(ref); st.Phase == PhaseExited {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	r2, _ := NewRunnerWithState(dir)
	if _, finished := r2.RecoverState(); finished != 1 {
		files, _ := os.ReadDir(dir)
		t.Fatalf("finished = %d, want 1 (state files: %d)", finished, len(files))
	}
	if st := r2.Probe(ref); st.Phase != PhaseExited || st.ExitCode != 3 {
		t.Fatalf("restored probe = %+v, want exited/3", st)
	}
}
