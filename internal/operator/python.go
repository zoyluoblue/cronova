package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// RunPython executes inline Python code with `python3 -c` (falling back to
// `python`), streaming stdout+stderr to out. The code is passed as an argv
// element, not through a shell, so it needs no quoting/escaping. The child
// inherits this process's env (the injected CRONOVA_* task vars), and ctx bounds
// it (the scheduler kills the run-op group on timeout/cancel). The exit code is
// the interpreter's, so a Python error/non-zero exit fails the task (retry-aware).
func RunPython(ctx context.Context, code string, out io.Writer) int {
	bin, err := exec.LookPath("python3")
	if err != nil {
		if bin, err = exec.LookPath("python"); err != nil {
			fmt.Fprintln(out, "python: no python3/python interpreter on PATH")
			return 1
		}
	}
	cmd := exec.CommandContext(ctx, bin, "-c", code)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(out, "python: %v\n", err)
		return 1
	}
	return 0
}
