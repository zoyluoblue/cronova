package executor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Runner is the shared subprocess engine behind both LocalExecutor (in-process)
// and the gRPC executor server. It launches each task in its own process group
// (so timeouts/cancels kill the whole tree), records the exit code, and answers
// Probe/Cancel. Launch is idempotent on ref (= Spec.TaskRunID).
//
// The registry is in-memory. On a GRACEFUL executor shutdown call Shutdown to
// kill the task process groups (otherwise they are reparented to init and leak,
// which — combined with the scheduler re-running a "lost" task — would break
// idempotency). On a HARD kill (-9) of the executor that cleanup cannot run, so
// orphans must be reaped by a process supervisor (e.g. systemd
// KillMode=control-group). Either way, a restarted executor answers Probe →
// PhaseUnknown for the old refs, which the scheduler treats as a lost task.
type Runner struct {
	mu    sync.Mutex
	tasks map[string]*procTask
}

type procTask struct {
	cmd      *exec.Cmd
	finished bool
	exitCode int
}

// NewRunner creates an empty Runner.
func NewRunner() *Runner {
	return &Runner{tasks: map[string]*procTask{}}
}

// Launch starts the task and returns its ref. If a task with the same ref is
// already known, it returns that ref without starting a second process.
func (r *Runner) Launch(spec Spec) (string, error) {
	ref := spec.TaskRunID
	if ref == "" {
		return "", errors.New("executor: empty TaskRunID")
	}

	r.mu.Lock()
	if _, ok := r.tasks[ref]; ok {
		r.mu.Unlock()
		return ref, nil // idempotent
	}
	t := &procTask{}
	r.tasks[ref] = t // reserve so concurrent Launches dedupe
	r.mu.Unlock()

	logFile, err := openLog(spec.LogPath)
	if err != nil {
		r.forget(ref)
		return "", fmt.Errorf("open log: %w", err)
	}
	fmt.Fprintf(logFile, "=== cronova task %s started at %s ===\n", ref, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(logFile, "$ %s\n", spec.Command)

	cmd := exec.Command("sh", "-c", spec.Command)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = buildEnv(spec.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(logFile, "=== launch error: %v ===\n", err)
		_ = logFile.Close()
		r.forget(ref)
		return "", fmt.Errorf("start: %w", err)
	}

	r.mu.Lock()
	t.cmd = cmd
	r.mu.Unlock()

	go r.wait(ref, cmd, logFile, spec.Timeout)
	return ref, nil
}

func (r *Runner) wait(ref string, cmd *exec.Cmd, logFile *os.File, timeout time.Duration) {
	defer logFile.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var code int
	if timeout > 0 {
		select {
		case <-time.After(timeout):
			killGroup(cmd)
			<-done
			code = TimeoutExitCode
			fmt.Fprintf(logFile, "=== killed: timeout after %s ===\n", timeout)
		case err := <-done:
			code = exitCode(err)
			fmt.Fprintf(logFile, "=== exited with code %d ===\n", code)
		}
	} else {
		code = exitCode(<-done)
		fmt.Fprintf(logFile, "=== exited with code %d ===\n", code)
	}

	r.mu.Lock()
	if t, ok := r.tasks[ref]; ok {
		t.finished = true
		t.exitCode = code
	}
	r.mu.Unlock()
}

// Probe reports the task's phase.
func (r *Runner) Probe(ref string) Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[ref]
	if !ok {
		return Status{Phase: PhaseUnknown}
	}
	if t.finished {
		return Status{Phase: PhaseExited, ExitCode: t.exitCode}
	}
	return Status{Phase: PhaseRunning}
}

// Cancel kills the task's process group if it is still running.
func (r *Runner) Cancel(ref string) error {
	r.mu.Lock()
	t, ok := r.tasks[ref]
	r.mu.Unlock()
	if !ok || t.cmd == nil {
		return nil
	}
	killGroup(t.cmd)
	return nil
}

func (r *Runner) forget(ref string) {
	r.mu.Lock()
	delete(r.tasks, ref)
	r.mu.Unlock()
}

// Shutdown kills the process group of every still-running task. Call it on a
// graceful executor shutdown so tasks are not left as orphans.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.tasks {
		if !t.finished && t.cmd != nil {
			killGroup(t.cmd)
		}
	}
}

// --- helpers ---

func openLog(path string) (*os.File, error) {
	if path == "" {
		return nil, errors.New("empty log path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

func buildEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // negative pid => process group
}
