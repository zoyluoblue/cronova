// Package executor runs a task's command as an OS subprocess, streaming its
// stdout/stderr to a per-instance log file.
//
// The executor is asynchronous: Launch starts a task and returns immediately
// with a ref; Probe reports the task's phase; Cancel kills it. This split is
// what makes scheduler crash recovery possible — after a restart the scheduler
// re-attaches to in-flight tasks by Probing their (deterministic) refs rather
// than owning the child processes directly. See docs/ARCHITECTURE.md §8–§9.
//
// The shared subprocess machinery lives in Runner. LocalExecutor wraps a Runner
// in-process; the gRPC server (cmd/cronova-executor) exposes a Runner over the
// network, and grpcClient dials it. All implement Executor.
package executor

import (
	"context"
	"time"
)

// Spec describes one task execution.
type Spec struct {
	TaskRunID string            // run_id + "/" + task_id; the idempotency key and ref
	Type      string            // shell/python/sql/jar (informational)
	Command   string            // shell command line
	Env       map[string]string // injected env (CRONOVA_* vars)
	Timeout   time.Duration     // 0 = no timeout
	LogPath   string            // file to write combined stdout/stderr
	Dir       string            // working directory (cmd.Dir); "" = inherit the executor's cwd
	// Redact holds resolved secret values (connection passwords) substituted into
	// this task. The executor masks every occurrence in EVERYTHING it writes to the
	// log — the "$ ..." echo and the child's own stdout/stderr — so a secret never
	// reaches the log even via a traceback or driver error.
	Redact []string
}

// Phase is a task's coarse execution state as reported by Probe.
type Phase int

const (
	// PhaseUnknown means the executor has no record of the ref: it was never
	// launched, or the executor itself restarted and lost it.
	PhaseUnknown Phase = iota
	PhaseRunning
	PhaseExited
)

// Status is the result of a Probe.
type Status struct {
	Phase    Phase
	ExitCode int // valid when Phase == PhaseExited; 124 indicates a timeout kill
}

// TimeoutExitCode marks a task killed for exceeding its timeout.
const TimeoutExitCode = 124

// Executor launches, probes, and cancels tasks. Launch is idempotent on
// Spec.TaskRunID.
type Executor interface {
	Launch(ctx context.Context, spec Spec) (ref string, err error)
	Probe(ctx context.Context, ref string) (Status, error)
	Cancel(ctx context.Context, ref string) error
}
