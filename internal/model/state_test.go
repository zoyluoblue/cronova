package model

import "testing"

func TestCanTaskTransition(t *testing.T) {
	legal := []struct{ from, to TaskState }{
		{TaskScheduled, TaskQueued},
		{TaskScheduled, TaskUpstreamFailed},
		{TaskScheduled, TaskSkipped},
		{TaskQueued, TaskRunning},
		{TaskRunning, TaskSuccess},
		{TaskRunning, TaskUpForRetry},
		{TaskRunning, TaskFailed},
		{TaskUpForRetry, TaskScheduled},
		{TaskRunning, TaskCancelled},   // cancel
		{TaskFailed, TaskScheduled},    // manual retry (clear) reactivates a terminal task
		{TaskCancelled, TaskScheduled}, // retry a cancelled task
	}
	for _, c := range legal {
		if !CanTaskTransition(c.from, c.to) {
			t.Errorf("expected legal transition %s -> %s", c.from, c.to)
		}
	}

	illegal := []struct{ from, to TaskState }{
		{TaskScheduled, TaskRunning}, // must go through queued
		{TaskScheduled, TaskSuccess},
		{TaskSuccess, TaskRunning}, // a terminal task reactivates to scheduled, not running
		{TaskRunning, TaskQueued},
		{TaskQueued, TaskSuccess},
	}
	for _, c := range illegal {
		if CanTaskTransition(c.from, c.to) {
			t.Errorf("expected illegal transition %s -> %s", c.from, c.to)
		}
	}
}

func TestTaskStateIsTerminal(t *testing.T) {
	terminal := []TaskState{TaskSuccess, TaskFailed, TaskUpstreamFailed, TaskSkipped}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	nonTerminal := []TaskState{TaskScheduled, TaskQueued, TaskRunning, TaskUpForRetry}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestEvalTriggerRule(t *testing.T) {
	S, F, R, K, U := TaskSuccess, TaskFailed, TaskRunning, TaskSkipped, TaskUpstreamFailed
	TO, C := TaskTimedOut, TaskCancelled
	cases := []struct {
		rule           string
		deps           []TaskState
		ready, blocked bool
	}{
		{RuleAllSuccess, []TaskState{S, S}, true, false},
		{RuleAllSuccess, []TaskState{S, F}, false, true},
		{RuleAllSuccess, []TaskState{S, R}, false, false}, // wait
		{RuleAllSuccess, []TaskState{S, K}, false, true},  // skipped breaks all_success
		// timed_out and cancelled are terminal non-successes: they must count as a
		// failure so a downstream is decisively blocked (never left waiting forever).
		{RuleAllSuccess, []TaskState{S, TO}, false, true}, // timed_out blocks all_success
		{RuleAllSuccess, []TaskState{S, C}, false, true},  // cancelled blocks all_success
		{RuleOneFailed, []TaskState{S, TO}, true, false},  // timed_out satisfies one_failed
		{RuleOneFailed, []TaskState{S, C}, true, false},   // cancelled satisfies one_failed
		{RuleAllFailed, []TaskState{TO, C}, true, false},  // both count as failed
		{RuleNoneFailed, []TaskState{S, TO}, false, true}, // timed_out is a failure
		{RuleAllDone, []TaskState{S, F}, true, false},     // runs despite a failure
		{RuleAllDone, []TaskState{S, R}, false, false},    // wait for all terminal
		{RuleAllDone, []TaskState{U, F}, true, false},
		{RuleOneSuccess, []TaskState{F, S}, true, false},
		{RuleOneSuccess, []TaskState{F, F}, false, true},
		{RuleOneFailed, []TaskState{S, F}, true, false},
		{RuleOneFailed, []TaskState{S, S}, false, true},
		{RuleAllFailed, []TaskState{F, F}, true, false},
		{RuleAllFailed, []TaskState{F, S}, false, true},
		{RuleNoneFailed, []TaskState{S, K}, true, false}, // skipped ok
		{RuleNoneFailed, []TaskState{S, F}, false, true},
		{RuleAllSuccess, nil, true, false}, // no deps -> always ready
		{RuleOneFailed, nil, true, false},
	}
	for i, c := range cases {
		ready, blocked := EvalTriggerRule(c.rule, c.deps)
		if ready != c.ready || blocked != c.blocked {
			t.Errorf("case %d %s %v: got (ready=%v,blocked=%v) want (%v,%v)", i, c.rule, c.deps, ready, blocked, c.ready, c.blocked)
		}
	}
}

func TestCanRunTransition(t *testing.T) {
	if !CanRunTransition(RunQueued, RunRunning) {
		t.Error("queued -> running should be legal")
	}
	if !CanRunTransition(RunRunning, RunSuccess) {
		t.Error("running -> success should be legal")
	}
	// cancel: an active run may be stopped
	if !CanRunTransition(RunRunning, RunCancelled) {
		t.Error("running -> cancelled should be legal")
	}
	// retry: a finished run reactivates to running (success/failed/cancelled → running)
	if !CanRunTransition(RunSuccess, RunRunning) || !CanRunTransition(RunFailed, RunRunning) || !CanRunTransition(RunCancelled, RunRunning) {
		t.Error("a finished run -> running should be legal (manual retry)")
	}
	if CanRunTransition(RunFailed, RunSuccess) {
		t.Error("failed -> success should be illegal")
	}
}
