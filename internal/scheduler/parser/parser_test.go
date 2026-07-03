package parser

import (
	"strings"
	"testing"
)

func TestParseValidDAG(t *testing.T) {
	raw := []byte(`
dag_id: daily_etl
schedule: "0 2 * * *"
start_date: 2026-06-01
catchup: true
max_active_runs: 2
default_retries: 2
tasks:
  - id: extract
    command: "echo extract"
    pool: default
    priority: 10
  - id: transform
    command: "echo transform"
    deps: [extract]
    retries: 3
  - id: load
    command: "echo load"
    deps: [transform]
`)
	d, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.DagID != "daily_etl" || d.MaxActiveRuns != 2 || !d.Catchup {
		t.Errorf("header mismatch: %+v", d)
	}
	if len(d.Tasks) != 3 {
		t.Fatalf("got %d tasks", len(d.Tasks))
	}
	// defaults + overrides
	if d.Tasks[0].Type != "shell" || d.Tasks[0].Pool != "default" {
		t.Errorf("task0 defaults wrong: %+v", d.Tasks[0])
	}
	if d.Tasks[0].Retries != 2 {
		t.Errorf("task0 should inherit default_retries=2, got %d", d.Tasks[0].Retries)
	}
	if d.Tasks[1].Retries != 3 {
		t.Errorf("task1 should override retries=3, got %d", d.Tasks[1].Retries)
	}
	if d.StartDate.Year() != 2026 || d.StartDate.Month() != 6 {
		t.Errorf("start_date wrong: %v", d.StartDate)
	}
}

func TestParseRejectsCycle(t *testing.T) {
	raw := []byte(`
dag_id: cyclic
tasks:
  - id: a
    command: "x"
    deps: [c]
  - id: b
    command: "x"
    deps: [a]
  - id: c
    command: "x"
    deps: [b]
`)
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestParseRejectsUnknownDep(t *testing.T) {
	raw := []byte(`
dag_id: bad
tasks:
  - id: a
    command: "x"
    deps: [ghost]
`)
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("expected unknown-dep error, got %v", err)
	}
}

func TestParseRejectsDuplicateID(t *testing.T) {
	raw := []byte(`
dag_id: dup
tasks:
  - id: a
    command: "x"
  - id: a
    command: "y"
`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestParseRejectsBadSchedule(t *testing.T) {
	raw := []byte(`
dag_id: bad_sched
schedule: "not a cron"
tasks:
  - id: a
    command: "x"
`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "schedule") {
		t.Fatalf("expected schedule error, got %v", err)
	}
}

// A 0-task "shell" DAG is valid: the builder creates the DAG first, then tasks
// are added incrementally. Such a DAG parses cleanly with an empty task set and
// is never scheduled/triggered (gated in the scheduler).
func TestParseAllowsEmptyTasks(t *testing.T) {
	d, err := Parse([]byte("dag_id: shell\n"))
	if err != nil {
		t.Fatalf("0-task DAG should parse, got %v", err)
	}
	if len(d.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(d.Tasks))
	}
	if d.DagID != "shell" {
		t.Errorf("dag_id = %q, want shell", d.DagID)
	}
}

// A shell DAG that also declares a schedule still parses; the scheduler is
// responsible for not creating runs for it.
func TestParseAllowsEmptyTasksWithSchedule(t *testing.T) {
	if _, err := Parse([]byte("dag_id: shell\nschedule: \"@every 1m\"\n")); err != nil {
		t.Fatalf("scheduled shell DAG should parse, got %v", err)
	}
}

func TestParseRejectsUnsafeDagID(t *testing.T) {
	for _, id := range []string{"../../etc/passwd", "a/b", "with space", "../evil"} {
		raw := []byte("dag_id: \"" + id + "\"\ntasks:\n  - id: a\n    command: \"x\"\n")
		if _, err := Parse(raw); err == nil {
			t.Errorf("expected rejection of unsafe dag_id %q", id)
		}
	}
	// Safe ids pass.
	if _, err := Parse([]byte("dag_id: daily_etl-v2.1\ntasks:\n  - id: a\n    command: \"x\"\n")); err != nil {
		t.Errorf("safe dag_id rejected: %v", err)
	}
}

func TestParseEveryDescriptor(t *testing.T) {
	raw := []byte(`
dag_id: ticker
schedule: "@every 10s"
tasks:
  - id: a
    command: "echo hi"
`)
	if _, err := Parse(raw); err != nil {
		t.Fatalf("@every should be valid: %v", err)
	}
}

func TestParseNotify(t *testing.T) {
	raw := []byte(`
dag_id: alertme
tasks:
  - id: a
    command: "echo hi"
notify:
  url: "https://hooks.slack.com/services/T/B/X"
  on: [failure, success]
`)
	d, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.NotifyURL != "https://hooks.slack.com/services/T/B/X" {
		t.Errorf("notify url = %q", d.NotifyURL)
	}
	if len(d.NotifyOn) != 2 || d.NotifyOn[0] != "failure" || d.NotifyOn[1] != "success" {
		t.Errorf("notify on = %v", d.NotifyOn)
	}
}

func TestParseRejectsBadNotify(t *testing.T) {
	bad := []byte(`
dag_id: bad
tasks:
  - id: a
    command: "echo hi"
notify:
  url: "ftp://nope"
  on: [failure]
`)
	if _, err := Parse(bad); err == nil || !strings.Contains(err.Error(), "http") {
		t.Errorf("bad notify url should error, got %v", err)
	}
	badEvent := []byte(`
dag_id: bad2
tasks:
  - id: a
    command: "echo hi"
notify:
  url: "https://x.test/hook"
  on: [failure, blowup]
`)
	if _, err := Parse(badEvent); err == nil || !strings.Contains(err.Error(), "notify.on") {
		t.Errorf("bad notify event should error, got %v", err)
	}
}
