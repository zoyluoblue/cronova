package executor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// finishedTaskTTL is how long a completed task's result is retained in the
// registry so a (possibly retried) Probe can still read its exit code. It is far
// longer than any scheduler probe interval, so the result is always consumed
// before eviction; without it the map would grow without bound for the life of
// the executor process.
const finishedTaskTTL = time.Hour

type procTask struct {
	cmd        *exec.Cmd
	finished   bool
	exitCode   int
	finishedAt time.Time
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
	r.sweepFinishedLocked(time.Now())
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
	// All log writes — the "$ ..." echo AND the child's stdout/stderr — go through
	// sink, which masks any resolved secret (connection password) so a value can
	// never reach the log, not even via a traceback or a driver error. When there
	// are no secrets, sink is the raw file (unbuffered, immediate writes).
	sink := newLogSink(logFile, spec.Redact)
	fmt.Fprintf(sink, "=== cronova task %s started at %s ===\n", ref, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(sink, "$ %s\n", spec.Command)

	cmd := exec.Command("sh", "-c", spec.Command)
	cmd.Stdout = sink
	cmd.Stderr = sink
	cmd.Env = buildEnv(spec.Env)
	cmd.Dir = spec.Dir // "" keeps the executor's cwd; else run in the staged project dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// A set working dir must exist ON THIS (the executor's) host. When it doesn't,
	// the usual cause is a project-attached task dispatched to an executor that
	// does NOT share the scheduler's filesystem (a remote gRPC executor) — the
	// staged copy lives on the scheduler host only. Fail with that, not a cryptic
	// "chdir: no such file or directory".
	if spec.Dir != "" {
		if fi, err := os.Stat(spec.Dir); err != nil || !fi.IsDir() {
			msg := fmt.Sprintf("working directory %q not found on the executor host — project attach requires the executor to share the scheduler's filesystem (in-process, or a same-host/shared-mount executor)", spec.Dir)
			fmt.Fprintf(sink, "=== launch error: %s ===\n", msg)
			_ = sink.Close()
			r.forget(ref)
			return "", errors.New(msg)
		}
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(sink, "=== launch error: %v ===\n", err)
		_ = sink.Close()
		r.forget(ref)
		return "", fmt.Errorf("start: %w", err)
	}

	r.mu.Lock()
	t.cmd = cmd
	r.mu.Unlock()

	go r.wait(ref, cmd, sink, spec.Timeout)
	return ref, nil
}

func (r *Runner) wait(ref string, cmd *exec.Cmd, sink logSink, timeout time.Duration) {
	defer sink.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var code int
	if timeout > 0 {
		select {
		case <-time.After(timeout):
			killGroup(cmd)
			<-done
			code = TimeoutExitCode
			fmt.Fprintf(sink, "=== killed: timeout after %s ===\n", timeout)
		case err := <-done:
			code = exitCode(err)
			fmt.Fprintf(sink, "=== exited with code %d ===\n", code)
		}
	} else {
		code = exitCode(<-done)
		fmt.Fprintf(sink, "=== exited with code %d ===\n", code)
	}

	r.mu.Lock()
	if t, ok := r.tasks[ref]; ok {
		t.finished = true
		t.exitCode = code
		t.finishedAt = time.Now()
	}
	r.mu.Unlock()
}

// sweepFinishedLocked evicts completed tasks whose result has outlived
// finishedTaskTTL. Caller must hold r.mu. Only finished entries are removed, so a
// still-running task (or one whose result a scheduler may yet Probe) is never
// dropped.
func (r *Runner) sweepFinishedLocked(now time.Time) {
	for ref, t := range r.tasks {
		if t.finished && now.Sub(t.finishedAt) > finishedTaskTTL {
			delete(r.tasks, ref)
		}
	}
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
	// Read t.cmd UNDER the lock — Launch writes it under the lock, and a concurrent
	// cancel (e.g. MarkTask killing a task the tick is still launching) would
	// otherwise race that write. A nil cmd means the process hasn't started yet;
	// runTask's post-launch guarded write then kills it once it does.
	r.mu.Lock()
	t, ok := r.tasks[ref]
	var cmd *exec.Cmd
	if ok {
		cmd = t.cmd
	}
	r.mu.Unlock()
	if cmd == nil {
		return nil
	}
	killGroup(cmd)
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

// --- log redaction ---

// logSink is the runner's write target for a task log. Both concrete sinks own
// the underlying file and close it on Close.
type logSink interface {
	io.Writer
	Close() error
}

// redactBufCap bounds the line buffer so a newline-less stream can't grow it
// without limit.
const redactBufCap = 1 << 20

// newLogSink returns a plain file sink when there is nothing to redact (writes
// pass straight through, unbuffered), or a redactWriter that masks every secret
// value in each completed line.
func newLogSink(f *os.File, redact []string) logSink {
	if len(redact) == 0 {
		return f
	}
	// Copy and sort by length descending so redactBytes masks the LONGEST match
	// first — otherwise a secret that is a prefix of another ("pass" vs "password")
	// would be replaced first and leave the longer one's tail exposed.
	secrets := append([]string(nil), redact...)
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	maxLen := 0
	for _, s := range secrets {
		if len(s) > maxLen {
			maxLen = len(s)
		}
	}
	return &redactWriter{w: f, secrets: secrets, maxLen: maxLen}
}

// redactWriter masks secret substrings in everything written to a task log. It
// buffers by line (a secret — a connection password — never contains a newline,
// so a line boundary is a safe place to redact without splitting a secret across
// two Write calls) and flushes each complete line redacted; the tail is flushed
// on Close. cmd.Stdout and cmd.Stderr share one sink and os/exec writes them from
// separate goroutines, so Write is mutex-guarded.
type redactWriter struct {
	w       *os.File
	secrets []string
	maxLen  int // longest secret; bounds the tail held back across a capped flush
	mu      sync.Mutex
	buf     []byte
}

func (rw *redactWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.buf = append(rw.buf, p...)
	for {
		i := bytes.IndexByte(rw.buf, '\n')
		if i < 0 {
			break
		}
		if _, err := rw.w.Write(redactBytes(rw.buf[:i+1], rw.secrets)); err != nil {
			return 0, err
		}
		rw.buf = rw.buf[i+1:]
	}
	// Bound memory for a pathological newline-less stream. Redact the WHOLE buffer
	// first (so every complete secret occurrence — including ones whose bytes also
	// start another secret — is masked), then emit all but the last maxLen-1 bytes.
	// A secret still being assembled dangles at most maxLen-1 bytes into the tail
	// and stays RAW in the redacted buffer (an incomplete match is not masked), so
	// re-redacting it with the next bytes still catches it. Only masked-or-non-secret
	// content is ever emitted — a complete secret is never split across the flush.
	if len(rw.buf) > redactBufCap {
		red := redactBytes(rw.buf, rw.secrets)
		keep := rw.maxLen - 1
		if keep > len(red) {
			keep = len(red)
		}
		if keep < 0 {
			keep = 0
		}
		if _, err := rw.w.Write(red[:len(red)-keep]); err != nil {
			return 0, err
		}
		rw.buf = append(rw.buf[:0], red[len(red)-keep:]...)
	}
	return len(p), nil
}

func (rw *redactWriter) Close() error {
	rw.mu.Lock()
	if len(rw.buf) > 0 {
		_, _ = rw.w.Write(redactBytes(rw.buf, rw.secrets))
		rw.buf = nil
	}
	rw.mu.Unlock()
	return rw.w.Close()
}

// redactMask replaces a secret occurrence in the log.
var redactMask = []byte("****")

// redactBytes masks every secret occurrence in b in a SINGLE left-to-right pass.
// `secrets` is pre-sorted longest-first (newLogSink), so the longest match wins at
// each position. A single simultaneous pass — rather than one bytes.ReplaceAll per
// secret — means an emitted mask is never re-scanned, so masking one secret can
// never resurface or reconstruct another.
//
// Known limitation: a secret composed of the mask character ("*") — e.g. a literal
// value of "**" — cannot be fully hidden by an asterisk mask (the mask contains
// it). Such a value is not a realistic connection password, so this is accepted.
func redactBytes(b []byte, secrets []string) []byte {
	// Fast path: leave b untouched (and un-allocated) when no secret is present —
	// the overwhelmingly common case.
	found := false
	for _, s := range secrets {
		if s != "" && bytes.Contains(b, []byte(s)) {
			found = true
			break
		}
	}
	if !found {
		return b
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		matched := false
		for _, s := range secrets {
			if s != "" && bytes.HasPrefix(b[i:], []byte(s)) {
				out = append(out, redactMask...)
				i += len(s)
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, b[i])
			i++
		}
	}
	return out
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
