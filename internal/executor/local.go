package executor

import "context"

// LocalExecutor runs tasks in the scheduler's own process via an embedded
// Runner. It is simple and dependency-free, ideal for tests and single-process
// deployments — but because the tasks are children of the scheduler, they do
// NOT survive a scheduler restart (Probe returns PhaseUnknown afterward). Use
// the gRPC executor (cmd/cronova-executor) when crash recovery matters.
type LocalExecutor struct {
	r *Runner
}

var _ Executor = (*LocalExecutor)(nil)

func NewLocal() *LocalExecutor { return &LocalExecutor{r: NewRunner()} }

func (e *LocalExecutor) Launch(_ context.Context, spec Spec) (string, error) {
	return e.r.Launch(spec)
}

func (e *LocalExecutor) Probe(_ context.Context, ref string) (Status, error) {
	return e.r.Probe(ref), nil
}

func (e *LocalExecutor) Cancel(_ context.Context, ref string) error {
	return e.r.Cancel(ref)
}
