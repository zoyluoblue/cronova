package model

// taskTransitions is the allowed task-instance state-machine graph. A
// transition from A to B is legal iff B is in taskTransitions[A].
//
// See docs/ARCHITECTURE.md §7.4 for the diagram this mirrors.
var taskTransitions = map[TaskState][]TaskState{
	TaskScheduled: {TaskQueued, TaskUpstreamFailed, TaskSkipped},
	// queued -> upstream_failed: an upstream task can fail while this one is
	//   still waiting in the pool queue, before the executor picks it up.
	// queued -> failed: the executor's Launch RPC failed, so the task never ran.
	TaskQueued:     {TaskRunning, TaskUpstreamFailed, TaskFailed},
	TaskRunning:    {TaskSuccess, TaskUpForRetry, TaskFailed},
	TaskUpForRetry: {TaskScheduled},
	// terminal states have no outgoing transitions
	TaskSuccess:        {},
	TaskFailed:         {},
	TaskUpstreamFailed: {},
	TaskSkipped:        {},
}

// CanTaskTransition reports whether a task may move from -> to.
func CanTaskTransition(from, to TaskState) bool {
	for _, allowed := range taskTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// runTransitions is the allowed DagRun state-machine graph.
//
// queued -> success/failed (without passing through running) are defensive:
// a run may resolve before any task runs (e.g. every task skipped, or the run
// is aborted while still queued). The normal path is queued -> running -> *.
var runTransitions = map[RunState][]RunState{
	RunQueued:  {RunRunning, RunSuccess, RunFailed},
	RunRunning: {RunSuccess, RunFailed},
	RunSuccess: {},
	RunFailed:  {},
}

// Trigger rules decide whether a task runs given its upstream (dependency)
// states. all_success is the default. See docs/ARCHITECTURE.md §12.
const (
	RuleAllSuccess = "all_success" // all deps succeeded (default)
	RuleAllDone    = "all_done"    // all deps finished, regardless of outcome
	RuleOneSuccess = "one_success" // at least one dep succeeded
	RuleOneFailed  = "one_failed"  // at least one dep failed
	RuleAllFailed  = "all_failed"  // all deps failed
	RuleNoneFailed = "none_failed" // all deps finished and none failed (success/skipped ok)
)

var validTriggerRules = map[string]bool{
	RuleAllSuccess: true, RuleAllDone: true, RuleOneSuccess: true,
	RuleOneFailed: true, RuleAllFailed: true, RuleNoneFailed: true,
}

// ValidTriggerRule reports whether r is a known trigger rule.
func ValidTriggerRule(r string) bool { return validTriggerRules[r] }

// EvalTriggerRule decides, given a task's dependency states and its trigger
// rule, whether the task is ready to dispatch and/or blocked (its branch can
// never satisfy the rule, so it should be marked upstream_failed). ready and
// blocked are mutually exclusive; a task that is neither simply waits. A task
// with no dependencies is always ready.
func EvalTriggerRule(rule string, deps []TaskState) (ready, blocked bool) {
	n := len(deps)
	if n == 0 {
		return true, false
	}
	succ, fail, skip, done := 0, 0, 0, 0
	for _, s := range deps {
		switch s {
		case TaskSuccess:
			succ++
		case TaskFailed, TaskUpstreamFailed:
			fail++
		case TaskSkipped:
			skip++
		}
		if s.IsTerminal() {
			done++
		}
	}
	allDone := done == n
	switch rule {
	case RuleAllDone:
		return allDone, false
	case RuleOneSuccess:
		return succ >= 1, allDone && succ == 0
	case RuleOneFailed:
		return fail >= 1, allDone && fail == 0
	case RuleAllFailed:
		return allDone && fail == n, succ > 0 || skip > 0
	case RuleNoneFailed:
		return allDone && fail == 0, fail > 0
	default: // all_success
		return succ == n, fail > 0 || skip > 0
	}
}

// CanRunTransition reports whether a run may move from -> to.
func CanRunTransition(from, to RunState) bool {
	for _, allowed := range runTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}
