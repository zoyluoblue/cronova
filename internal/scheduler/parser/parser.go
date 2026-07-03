// Package parser turns a YAML workflow definition into a validated model.DAG.
// Validation covers: non-empty dag_id, unique task ids, dependencies that
// reference existing tasks, acyclicity, and a parseable cron schedule. A DAG may
// have zero tasks (a "shell" created by the builder before tasks are added); it
// is valid to store but never scheduled/triggered until it has a task. See
// docs/ARCHITECTURE.md §14 for the YAML spec.
package parser

import (
	"fmt"
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

type taskYAML struct {
	ID          string   `yaml:"id"`
	Type        string   `yaml:"type"`
	Command     string   `yaml:"command"`
	Deps        []string `yaml:"deps"`
	Pool        string   `yaml:"pool"`
	Priority    int      `yaml:"priority"`
	Retries     *int     `yaml:"retries"`     // pointer: distinguishes "unset" from 0
	RetryDelay  *int     `yaml:"retry_delay"` // seconds
	Timeout     int      `yaml:"timeout"`     // seconds
	TriggerRule string   `yaml:"trigger_rule"`
}

type dagYAML struct {
	DagID             string     `yaml:"dag_id"`
	Schedule          string     `yaml:"schedule"`
	StartDate         string     `yaml:"start_date"`
	Catchup           bool       `yaml:"catchup"`
	MaxActiveRuns     int        `yaml:"max_active_runs"`
	DefaultRetries    int        `yaml:"default_retries"`
	DefaultRetryDelay int        `yaml:"default_retry_delay"`
	Tasks             []taskYAML `yaml:"tasks"`
	TriggerAfter      []struct {
		DagID string `yaml:"dag_id"`
	} `yaml:"trigger_after"`
	Notify struct {
		URL string   `yaml:"url"`
		On  []string `yaml:"on"` // "failure", "success"
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
	var y dagYAML
	if err := yaml.Unmarshal(raw, &y); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if y.DagID == "" {
		return nil, fmt.Errorf("dag_id is required")
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

	maxActive := y.MaxActiveRuns
	if maxActive <= 0 {
		maxActive = 1
	}

	startDate, err := parseStartDate(y.StartDate)
	if err != nil {
		return nil, fmt.Errorf("dag %q: %w", y.DagID, err)
	}

	d := &model.DAG{
		DagID:          y.DagID,
		Schedule:       y.Schedule,
		StartDate:      startDate,
		Catchup:        y.Catchup,
		MaxActiveRuns:  maxActive,
		DefaultRetries: y.DefaultRetries,
		DefinitionYAML: string(raw),
	}
	for _, ta := range y.TriggerAfter {
		if ta.DagID != "" {
			d.TriggerAfter = append(d.TriggerAfter, ta.DagID)
		}
	}
	// notify: an outbound webhook fired when a run finishes in a listed state.
	d.NotifyURL = strings.TrimSpace(y.Notify.URL)
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

	seen := make(map[string]bool, len(y.Tasks))
	for _, t := range y.Tasks {
		if t.ID == "" {
			return nil, fmt.Errorf("dag %q: a task has an empty id", y.DagID)
		}
		if !idPattern.MatchString(t.ID) {
			return nil, fmt.Errorf("dag %q: invalid task id %q", y.DagID, t.ID)
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("dag %q: duplicate task id %q", y.DagID, t.ID)
		}
		seen[t.ID] = true

		task := model.Task{
			ID:          t.ID,
			Type:        orDefault(t.Type, "shell"),
			Command:     t.Command,
			Deps:        t.Deps,
			Pool:        orDefault(t.Pool, model.DefaultPoolName),
			Priority:    t.Priority,
			Retries:     y.DefaultRetries,
			RetryDelay:  y.DefaultRetryDelay,
			Timeout:     t.Timeout,
			TriggerRule: orDefault(t.TriggerRule, model.RuleAllSuccess),
		}
		if !model.ValidTriggerRule(task.TriggerRule) {
			return nil, fmt.Errorf("dag %q: task %q has invalid trigger_rule %q", y.DagID, t.ID, t.TriggerRule)
		}
		if t.Retries != nil {
			task.Retries = *t.Retries
		}
		if t.RetryDelay != nil {
			task.RetryDelay = *t.RetryDelay
		}
		if task.Command == "" {
			return nil, fmt.Errorf("dag %q: task %q has empty command", y.DagID, t.ID)
		}
		d.Tasks = append(d.Tasks, task)
	}

	// deps must reference existing tasks
	for _, t := range d.Tasks {
		for _, dep := range t.Deps {
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
