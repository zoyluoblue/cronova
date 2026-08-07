package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Attempt state persistence: with a state dir configured (the standalone
// cronova-executor does this), every launched attempt writes a small JSON
// record — pid, process group, and eventually the exit code (which the shell
// wrapper also writes to a sidecar file, so it survives the executor itself
// dying). On restart, RecoverState re-adopts live process groups and reports
// real exit codes for finished ones, instead of answering PhaseUnknown and
// costing every in-flight task a retry.

type attemptState struct {
	Ref       string    `json:"ref"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"started_at"`
	// Finished/ExitCode are recorded by wait() when the executor is alive at
	// exit time; after an executor crash the sidecar .exit file is the source.
	Finished bool `json:"finished"`
	ExitCode int  `json:"exit_code"`
}

func stateFile(dir, ref string) string {
	return filepath.Join(dir, strings.NewReplacer("/", "_", string(os.PathSeparator), "_").Replace(ref)+".json")
}

// exitFile is the sidecar the shell wrapper writes the exit code to; it exists
// so the code survives even when the executor process (and its wait goroutine)
// died before the task finished.
func exitFile(dir, ref string) string { return stateFile(dir, ref) + ".exit" }

func (r *Runner) saveState(st attemptState) {
	if r.stateDir == "" {
		return
	}
	b, _ := json.Marshal(st)
	_ = os.WriteFile(stateFile(r.stateDir, st.Ref), b, 0o600)
}

func (r *Runner) removeState(ref string) {
	if r.stateDir == "" {
		return
	}
	_ = os.Remove(stateFile(r.stateDir, ref))
	_ = os.Remove(exitFile(r.stateDir, ref))
}

// RecoverState scans the state dir and re-adopts previous attempts:
//   - finished (state or sidecar carries an exit code) → registered so Probe
//     answers the REAL exit code;
//   - process group still alive → adopted; a watcher polls for its exit and
//     reads the sidecar exit code when it lands;
//   - gone without a recorded code → registered as failed (exit -1): the
//     honest answer when nothing can prove how it ended.
func (r *Runner) RecoverState() (adopted, finished int) {
	if r.stateDir == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(r.stateDir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.stateDir, e.Name()))
		if err != nil {
			continue
		}
		var st attemptState
		if json.Unmarshal(b, &st) != nil || st.Ref == "" {
			_ = os.Remove(filepath.Join(r.stateDir, e.Name()))
			continue
		}
		if !st.Finished {
			if code, ok := readExitFile(exitFile(r.stateDir, st.Ref)); ok {
				st.Finished, st.ExitCode = true, code
			}
		}
		r.mu.Lock()
		if _, exists := r.tasks[st.Ref]; exists {
			r.mu.Unlock()
			continue
		}
		switch {
		case st.Finished:
			r.tasks[st.Ref] = &procTask{finished: true, exitCode: st.ExitCode, finishedAt: time.Now(), pgid: st.PGID}
			finished++
		case processGroupAlive(st.PGID):
			t := &procTask{pgid: st.PGID}
			r.tasks[st.Ref] = t
			go r.watchAdopted(st.Ref, st.PGID)
			adopted++
		default:
			// vanished without a recorded exit: cannot prove success — report failure
			r.tasks[st.Ref] = &procTask{finished: true, exitCode: -1, finishedAt: time.Now(), pgid: st.PGID}
			st.Finished, st.ExitCode = true, -1
			finished++
		}
		r.mu.Unlock()
		st.Finished = r.tasks[st.Ref].finished
		st.ExitCode = r.tasks[st.Ref].exitCode
		r.saveState(st)
	}
	return adopted, finished
}

// watchAdopted polls an adopted (re-attached) process group until it exits,
// then records the sidecar exit code (or -1 if the wrapper couldn't write one).
func (r *Runner) watchAdopted(ref string, pgid int) {
	for processGroupAlive(pgid) {
		time.Sleep(500 * time.Millisecond)
	}
	code := -1
	if c, ok := readExitFile(exitFile(r.stateDir, ref)); ok {
		code = c
	}
	r.mu.Lock()
	if t, ok := r.tasks[ref]; ok && !t.finished {
		t.finished = true
		t.exitCode = code
		t.finishedAt = time.Now()
	}
	r.mu.Unlock()
	r.saveState(attemptState{Ref: ref, PGID: pgid, Finished: true, ExitCode: code})
}

func readExitFile(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	var code int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &code); err != nil {
		return 0, false
	}
	return code, true
}

// processGroupAlive reports whether any process in the group still exists.
func processGroupAlive(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	return syscall.Kill(-pgid, 0) == nil
}
