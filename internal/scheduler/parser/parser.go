// Package parser turns a YAML workflow definition into a validated model.DAG.
// Validation covers: non-empty dag_id, unique task ids, dependencies that
// reference existing tasks, acyclicity, and a parseable cron schedule. A DAG may
// have zero tasks (a "shell" created by the builder before tasks are added); it
// is valid to store but never scheduled/triggered until it has a task. See
// docs/ARCHITECTURE.md §14 for the YAML spec.
package parser

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/zoyluo/cronova/internal/model"
	"gopkg.in/yaml.v3"
)

// idPattern restricts dag_id and task ids to safe identifier characters. This
// also prevents path traversal: dag_id is used as a filename when a DAG is
// created via the API (see scheduler.CreateDAG).
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

const (
	MaxDefinitionBytes = 1 << 20
	MaxTasks           = 1000
	MaxActiveRuns      = 1000
	maxIdentifierBytes = 128
	maxDependencies    = 10000
	maxDepsPerTask     = 256
	maxRetries         = 100
	maxDurationSeconds = 365 * 24 * 3600
	maxCommandBytes    = 256 << 10
)

type taskYAML struct {
	ID            string   `yaml:"id"`
	Type          string   `yaml:"type"`
	Command       string   `yaml:"command"`
	Deps          []string `yaml:"deps"`
	Pool          string   `yaml:"pool"`
	Priority      int      `yaml:"priority"`
	Retries       *int     `yaml:"retries"`         // pointer: distinguishes "unset" from 0
	RetryDelay    *int     `yaml:"retry_delay"`     // seconds
	RetryBackoff  string   `yaml:"retry_backoff"`   // "" | fixed | exponential
	RetryDelayMax int      `yaml:"retry_delay_max"` // seconds; caps exponential growth
	Timeout       int      `yaml:"timeout"`         // seconds
	SLA           int      `yaml:"sla"`             // seconds from run start (soft alert)
	TriggerRule   string   `yaml:"trigger_rule"`
	Conn          string   `yaml:"conn"`    // connection id for type: sql
	Project       string   `yaml:"project"` // uploaded project dir to stage as cwd (shell tasks)
	HTTP          *struct {
		Method         string            `yaml:"method"`
		URL            string            `yaml:"url"`
		Headers        map[string]string `yaml:"headers"`
		Body           string            `yaml:"body"`
		ExpectedStatus []int             `yaml:"expected_status"`
	} `yaml:"http"`
}

type dagYAML struct {
	DagID             string     `yaml:"dag_id"`
	Schedule          string     `yaml:"schedule"`
	StartDate         string     `yaml:"start_date"`
	Catchup           bool       `yaml:"catchup"`
	MaxActiveRuns     int        `yaml:"max_active_runs"`
	DefaultRetries    int        `yaml:"default_retries"`
	DefaultRetryDelay int        `yaml:"default_retry_delay"`
	SLA               int        `yaml:"sla"`            // run soft deadline, seconds from start
	DagrunTimeout     int        `yaml:"dagrun_timeout"` // run hard deadline, seconds from start
	Tasks             []taskYAML `yaml:"tasks"`
	TriggerAfter      []struct {
		DagID string `yaml:"dag_id"`
	} `yaml:"trigger_after"`
	Notify struct {
		URL    string   `yaml:"url"`
		On     []string `yaml:"on"`     // "failure", "success"
		Format string   `yaml:"format"` // ""/raw | slack | feishu | dingtalk
	} `yaml:"notify"`
}

// CronParser parses standard 5-field cron plus @descriptors and @every.
var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ParseSchedule parses a schedule string into a cron.Schedule. An empty string
// is an error here; callers should check for "" (manual/event-only) first.
func ParseSchedule(spec string) (cron.Schedule, error) {
	return cronParser.Parse(spec)
}

// Parse parses and validates a DAG definition. The raw bytes are retained in
// DefinitionYAML for UI round-tripping.
func Parse(raw []byte) (*model.DAG, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("yaml: empty definition")
	}
	if len(raw) > MaxDefinitionBytes {
		return nil, fmt.Errorf("yaml: definition exceeds %d bytes", MaxDefinitionBytes)
	}
	var y dagYAML
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&y); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("yaml: multiple documents are not allowed")
		}
		return nil, fmt.Errorf("yaml: trailing document: %w", err)
	}
	if y.DagID == "" {
		return nil, fmt.Errorf("dag_id is required")
	}
	if len(y.DagID) > maxIdentifierBytes {
		return nil, fmt.Errorf("dag_id exceeds %d bytes", maxIdentifierBytes)
	}
	if !idPattern.MatchString(y.DagID) {
		return nil, fmt.Errorf("invalid dag_id %q: use letters, digits, '_', '-', '.'", y.DagID)
	}
	// A DAG may legitimately have zero tasks: the builder creates a "shell" DAG
	// first, then tasks are added incrementally. A 0-task DAG is valid to store
	// but is never scheduled or triggered (gated in the scheduler) until it has
	// at least one task.
	if y.Schedule != "" {
		if _, err := ParseSchedule(y.Schedule); err != nil {
			return nil, fmt.Errorf("dag %q: invalid schedule %q: %w", y.DagID, y.Schedule, err)
		}
	}

	if y.MaxActiveRuns < 0 || y.MaxActiveRuns > MaxActiveRuns {
		return nil, fmt.Errorf("dag %q: max_active_runs must be between 1 and %d when set", y.DagID, MaxActiveRuns)
	}
	maxActive := y.MaxActiveRuns
	if maxActive == 0 {
		maxActive = 1
	}

	startDate, err := parseStartDate(y.StartDate)
	if err != nil {
		return nil, fmt.Errorf("dag %q: %w", y.DagID, err)
	}

	if y.DefaultRetries < 0 || y.DefaultRetries > maxRetries {
		return nil, fmt.Errorf("dag %q: default_retries must be between 0 and %d", y.DagID, maxRetries)
	}
	if y.DefaultRetryDelay < 0 || y.DefaultRetryDelay > maxDurationSeconds {
		return nil, fmt.Errorf("dag %q: default_retry_delay must be between 0 and %d seconds", y.DagID, maxDurationSeconds)
	}
	if y.SLA < 0 || y.SLA > maxDurationSeconds || y.DagrunTimeout < 0 || y.DagrunTimeout > maxDurationSeconds {
		return nil, fmt.Errorf("dag %q: sla/dagrun_timeout must be between 0 and %d seconds", y.DagID, maxDurationSeconds)
	}
	if len(y.Tasks) > MaxTasks {
		return nil, fmt.Errorf("dag %q: task count exceeds %d", y.DagID, MaxTasks)
	}
	if len(y.TriggerAfter) > MaxTasks {
		return nil, fmt.Errorf("dag %q: trigger_after count exceeds %d", y.DagID, MaxTasks)
	}
	d := &model.DAG{
		DagID:          y.DagID,
		Schedule:       y.Schedule,
		StartDate:      startDate,
		Catchup:        y.Catchup,
		MaxActiveRuns:  maxActive,
		DefaultRetries: y.DefaultRetries,
		SLA:            y.SLA,
		DagrunTimeout:  y.DagrunTimeout,
		DefinitionYAML: string(raw),
	}
	seenTriggers := map[string]bool{}
	for _, ta := range y.TriggerAfter {
		if ta.DagID != "" {
			if len(ta.DagID) > maxIdentifierBytes || !idPattern.MatchString(ta.DagID) {
				return nil, fmt.Errorf("dag %q: invalid trigger_after dag_id %q", y.DagID, ta.DagID)
			}
			if seenTriggers[ta.DagID] {
				return nil, fmt.Errorf("dag %q: duplicate trigger_after dag_id %q", y.DagID, ta.DagID)
			}
			seenTriggers[ta.DagID] = true
			d.TriggerAfter = append(d.TriggerAfter, ta.DagID)
		}
	}
	// notify: an outbound webhook fired when a run finishes in a listed state.
	d.NotifyURL = strings.TrimSpace(y.Notify.URL)
	if len(d.NotifyURL) > 8192 {
		return nil, fmt.Errorf("dag %q: notify.url exceeds 8192 bytes", y.DagID)
	}
	for _, ev := range y.Notify.On {
		ev = strings.TrimSpace(ev)
		if ev == "failure" || ev == "success" {
			d.NotifyOn = append(d.NotifyOn, ev)
		} else if ev != "" {
			return nil, fmt.Errorf("dag %q: invalid notify.on %q (want failure or success)", y.DagID, ev)
		}
	}
	if d.NotifyURL != "" {
		// scheme is case-insensitive (RFC 3986) — match the console's client check.
		lower := strings.ToLower(d.NotifyURL)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			return nil, fmt.Errorf("dag %q: notify.url must be http(s)", y.DagID)
		}
	}
	switch y.Notify.Format {
	case "", "raw", "slack", "feishu", "dingtalk":
		d.NotifyFormat = y.Notify.Format
	default:
		return nil, fmt.Errorf("dag %q: invalid notify.format %q (raw, slack, feishu, or dingtalk)", y.DagID, y.Notify.Format)
	}

	seen := make(map[string]bool, len(y.Tasks))
	totalDeps := 0
	for _, t := range y.Tasks {
		if t.ID == "" {
			return nil, fmt.Errorf("dag %q: a task has an empty id", y.DagID)
		}
		if len(t.ID) > maxIdentifierBytes || !idPattern.MatchString(t.ID) {
			return nil, fmt.Errorf("dag %q: invalid task id %q", y.DagID, t.ID)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("dag %q: duplicate task id %q", y.DagID, t.ID)
		}
		seen[t.ID] = true

		taskType := orDefault(strings.TrimSpace(t.Type), "shell")
		switch taskType {
		case "shell", "python", "sql", "jar", "http":
		default:
			return nil, fmt.Errorf("dag %q: task %q has unsupported type %q", y.DagID, t.ID, taskType)
		}
		if len(t.Deps) > maxDepsPerTask {
			return nil, fmt.Errorf("dag %q: task %q has more than %d dependencies", y.DagID, t.ID, maxDepsPerTask)
		}
		totalDeps += len(t.Deps)
		if totalDeps > maxDependencies {
			return nil, fmt.Errorf("dag %q: dependency count exceeds %d", y.DagID, maxDependencies)
		}
		if len(t.Command) > maxCommandBytes {
			return nil, fmt.Errorf("dag %q: task %q command exceeds %d bytes", y.DagID, t.ID, maxCommandBytes)
		}
		pool := orDefault(strings.TrimSpace(t.Pool), model.DefaultPoolName)
		if len(pool) > maxIdentifierBytes || !idPattern.MatchString(pool) {
			return nil, fmt.Errorf("dag %q: task %q has invalid pool %q", y.DagID, t.ID, pool)
		}
		task := model.Task{
			ID:          t.ID,
			Type:        taskType,
			Command:     t.Command,
			Deps:        t.Deps,
			Pool:        pool,
			Priority:    t.Priority,
			Retries:     y.DefaultRetries,
			RetryDelay:  y.DefaultRetryDelay,
			Timeout:     t.Timeout,
			SLA:         t.SLA,
			Conn:        strings.TrimSpace(t.Conn),
			Project:     strings.TrimSpace(t.Project),
			TriggerRule: orDefault(t.TriggerRule, model.RuleAllSuccess),
		}
		if !model.ValidTriggerRule(task.TriggerRule) {
			return nil, fmt.Errorf("dag %q: task %q has invalid trigger_rule %q", y.DagID, t.ID, t.TriggerRule)
		}
		if t.Timeout < 0 || t.Timeout > maxDurationSeconds || t.SLA < 0 || t.SLA > maxDurationSeconds {
			return nil, fmt.Errorf("dag %q: task %q timeout/sla must be between 0 and %d seconds", y.DagID, t.ID, maxDurationSeconds)
		}
		if !model.ValidRetryBackoff(t.RetryBackoff) {
			return nil, fmt.Errorf("dag %q: task %q has invalid retry_backoff %q (use fixed or exponential)", y.DagID, t.ID, t.RetryBackoff)
		}
		if t.RetryDelayMax < 0 {
			return nil, fmt.Errorf("dag %q: task %q retry_delay_max must be >= 0 seconds", y.DagID, t.ID)
		}
		// 30 days caps both delays: far past any real-world retry, and keeps the
		// exponential shift-math safely inside int64.
		const maxDelaySec = 30 * 24 * 3600
		if (t.RetryDelay != nil && *t.RetryDelay > maxDelaySec) || t.RetryDelayMax > maxDelaySec {
			return nil, fmt.Errorf("dag %q: task %q retry_delay/retry_delay_max must be <= %d seconds (30 days)", y.DagID, t.ID, maxDelaySec)
		}
		task.RetryBackoff = t.RetryBackoff
		task.RetryDelayMax = t.RetryDelayMax
		if t.Retries != nil {
			task.Retries = *t.Retries
		}
		if t.RetryDelay != nil {
			task.RetryDelay = *t.RetryDelay
		}
		if task.Retries < 0 || task.Retries > maxRetries {
			return nil, fmt.Errorf("dag %q: task %q retries must be between 0 and %d", y.DagID, t.ID, maxRetries)
		}
		if task.RetryDelay < 0 {
			return nil, fmt.Errorf("dag %q: task %q retry_delay must be >= 0 seconds", y.DagID, t.ID)
		}
		if task.Type == "http" {
			// an http task carries a request spec instead of a shell command.
			if t.HTTP == nil || strings.TrimSpace(t.HTTP.URL) == "" {
				return nil, fmt.Errorf("dag %q: http task %q requires http.url", y.DagID, t.ID)
			}
			for _, code := range t.HTTP.ExpectedStatus {
				if code < 100 || code > 599 {
					return nil, fmt.Errorf("dag %q: task %q invalid expected_status %d", y.DagID, t.ID, code)
				}
			}
			if len(t.HTTP.URL) > 8192 || len(t.HTTP.Body) > maxCommandBytes || len(t.HTTP.Headers) > 100 || len(t.HTTP.ExpectedStatus) > 100 {
				return nil, fmt.Errorf("dag %q: task %q http specification exceeds limits", y.DagID, t.ID)
			}
			task.HTTP = &model.HTTPSpec{
				Method: t.HTTP.Method, URL: strings.TrimSpace(t.HTTP.URL),
				Headers: t.HTTP.Headers, Body: t.HTTP.Body, ExpectedStatus: t.HTTP.ExpectedStatus,
			}
		} else if t.HTTP != nil {
			return nil, fmt.Errorf("dag %q: non-http task %q cannot define http", y.DagID, t.ID)
		} else if task.Command == "" {
			// shell/python/sql/jar all carry code/query/command in Command.
			return nil, fmt.Errorf("dag %q: task %q has empty command", y.DagID, t.ID)
		}
		if task.Type == "sql" && task.Conn == "" {
			return nil, fmt.Errorf("dag %q: sql task %q requires a conn (connection id)", y.DagID, t.ID)
		}
		d.Tasks = append(d.Tasks, task)
	}

	// deps must reference existing tasks
	for _, t := range d.Tasks {
		seenDeps := map[string]bool{}
		for _, dep := range t.Deps {
			if seenDeps[dep] {
				return nil, fmt.Errorf("dag %q: task %q has duplicate dependency %q", y.DagID, t.ID, dep)
			}
			seenDeps[dep] = true
			if !seen[dep] {
				return nil, fmt.Errorf("dag %q: task %q depends on unknown task %q", y.DagID, t.ID, dep)
			}
			if dep == t.ID {
				return nil, fmt.Errorf("dag %q: task %q depends on itself", y.DagID, t.ID)
			}
		}
	}

	if err := detectCycle(d.Tasks); err != nil {
		return nil, fmt.Errorf("dag %q: %w", y.DagID, err)
	}
	return d, nil
}

func parseStartDate(s string) (time.Time, error) {
	if s == "" {
		return time.Now().UTC().Truncate(24 * time.Hour), nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid start_date %q", s)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// detectCycle runs a DFS three-coloring over the dependency graph (edge
// task -> dep). A gray node reached again indicates a cycle.
func detectCycle(tasks []model.Task) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	adj := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		adj[t.ID] = t.Deps
	}
	color := make(map[string]int, len(tasks))

	var visit func(string, []string) error
	visit = func(n string, path []string) error {
		color[n] = gray
		path = append(path, n)
		for _, m := range adj[n] {
			switch color[m] {
			case gray:
				return fmt.Errorf("dependency cycle detected: %v -> %s", path, m)
			case white:
				if err := visit(m, path); err != nil {
					return err
				}
			}
		}
		color[n] = black
		return nil
	}

	for _, t := range tasks {
		if color[t.ID] == white {
			if err := visit(t.ID, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
