// Package model defines cronova's core domain types: DAGs, runs, task
// instances, pools, and the state/trigger enumerations they use.
package model

import (
	"errors"
	"time"
)

// ErrNoTasks is returned when an operation requires a DAG to have at least one
// task (e.g. a manual trigger) but the DAG is an empty shell. The API maps it to
// a 400 client error.
var ErrNoTasks = errors.New("dag has no tasks")

// ErrActiveRuns is returned when an operation (e.g. delete) is refused because
// the DAG still has queued/running runs. The API maps it to a 409 conflict.
var ErrActiveRuns = errors.New("dag has active runs")

// ErrRunNotActive is returned when a cancel is requested on a run that is already
// terminal (nothing to stop). The API maps it to a 409 conflict.
var ErrRunNotActive = errors.New("run is not active")

// ErrNothingToRetry is returned when a run-level retry finds no failed tasks. The
// API maps it to a 409 conflict.
var ErrNothingToRetry = errors.New("run has no failed tasks to retry")

// ErrRunStillActive is returned when a retry is requested on a run that is still
// queued/running (retry only a finished run). The API maps it to a 409 conflict.
var ErrRunStillActive = errors.New("run is still active — cancel it before retrying")

// ErrBadMarkState is returned when a manual mark requests a state that is not a
// legal target (task: success/failed/skipped; run: success/failed). The API maps
// it to a 400 client error.
var ErrBadMarkState = errors.New("invalid mark state")

// RunState is the lifecycle state of a DagRun.
type RunState string

const (
	RunQueued    RunState = "queued"
	RunRunning   RunState = "running"
	RunSuccess   RunState = "success"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled" // user-initiated stop (distinct from a failure)
	RunTimedOut  RunState = "timed_out" // exceeded the DAG's dagrun_timeout (distinct from a failure)
)

// IsTerminal reports whether the run state is final (no further scheduling).
func (s RunState) IsTerminal() bool {
	return s == RunSuccess || s == RunFailed || s == RunCancelled || s == RunTimedOut
}

// TaskState is the lifecycle state of a TaskInstance.
type TaskState string

const (
	TaskScheduled      TaskState = "scheduled"
	TaskQueued         TaskState = "queued"
	TaskRunning        TaskState = "running"
	TaskSuccess        TaskState = "success"
	TaskFailed         TaskState = "failed"
	TaskUpForRetry     TaskState = "up_for_retry"
	TaskUpstreamFailed TaskState = "upstream_failed"
	TaskSkipped        TaskState = "skipped"
	TaskCancelled      TaskState = "cancelled" // killed by a run cancellation
	TaskTimedOut       TaskState = "timed_out" // killed by the run's dagrun_timeout
)

// IsTerminal reports whether the task state is final (no further transitions).
func (s TaskState) IsTerminal() bool {
	switch s {
	case TaskSuccess, TaskFailed, TaskUpstreamFailed, TaskSkipped, TaskCancelled, TaskTimedOut:
		return true
	default:
		return false
	}
}

// TriggerType records what caused a DagRun to be created.
type TriggerType string

const (
	TriggerSchedule   TriggerType = "schedule"
	TriggerManual     TriggerType = "manual"
	TriggerDependency TriggerType = "dependency"
	TriggerEvent      TriggerType = "event"
	TriggerBackfill   TriggerType = "backfill" // operator-requested historical re-run
)

// DAG is a workflow definition. Persisted fields live in the dags table; Tasks
// are derived by parsing DefinitionYAML and are not stored row-by-row.
type DAG struct {
	DagID          string     `json:"dag_id"`
	Schedule       string     `json:"schedule"` // cron expression; empty => manual/event only
	StartDate      time.Time  `json:"start_date"`
	Catchup        bool       `json:"catchup"`
	Paused         bool       `json:"paused"`
	MaxActiveRuns  int        `json:"max_active_runs"`
	DefaultRetries int        `json:"default_retries"` // DAG-level default; per-task retries override
	DefinitionYAML string     `json:"definition_yaml,omitempty"`
	Owner          string     `json:"owner,omitempty"`   // reserved for future RBAC
	Project        string     `json:"project,omitempty"` // reserved for future RBAC
	Tasks          []Task     `json:"tasks,omitempty"`
	TriggerAfter   []string   `json:"trigger_after,omitempty"`  // upstream dag_ids
	NotifyURL      string     `json:"notify_url,omitempty"`     // webhook POSTed on a notify_on state
	NotifyOn       []string   `json:"notify_on,omitempty"`      // run states to notify on: "failure", "success"
	NotifyFormat   string     `json:"notify_format,omitempty"`  // webhook body shape: ""/raw | slack | feishu | dingtalk
	SLA            int        `json:"sla,omitempty"`            // soft deadline (seconds from run start); breach alerts, run keeps going
	DagrunTimeout  int        `json:"dagrun_timeout,omitempty"` // hard deadline (seconds from run start); breach kills the run → timed_out
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"` // non-nil => soft-deleted (archived)
}

// Task is a single node in a DAG.
type Task struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"` // shell/python/sql/jar/...
	Command    string   `json:"command"`
	Deps       []string `json:"deps,omitempty"`
	Pool       string   `json:"pool"`
	Priority   int      `json:"priority"`
	Retries    int      `json:"retries"`
	RetryDelay int      `json:"retry_delay"` // seconds (the base delay under exponential backoff)
	// RetryBackoff selects how the wait grows between attempts: "" or "fixed"
	// waits RetryDelay every time; "exponential" waits RetryDelay·2^(n-1) before
	// the n-th retry, capped by RetryDelayMax when set.
	RetryBackoff  string `json:"retry_backoff,omitempty"`
	RetryDelayMax int    `json:"retry_delay_max,omitempty"` // seconds; caps exponential growth (0 = uncapped)
	Timeout       int    `json:"timeout"`                   // execution timeout seconds; 0 = none (kills the attempt)
	SLA           int    `json:"sla,omitempty"`             // soft deadline (seconds from run start); breach alerts only
	TriggerRule   string `json:"trigger_rule"`              // when to run vs. upstream states (default all_success)
	// HTTP is set when Type == "http": a native HTTP request run via `cronova run-op`
	// instead of a shell Command. URL/Headers/Body may contain {{ var. }}/{{ conn. }}
	// templates, resolved server-side at dispatch.
	HTTP *HTTPSpec `json:"http,omitempty"`
	// Conn is the connection id for a Type == "sql" task; the connection's type
	// selects the driver and its host/port/login/password build the DSN. For
	// python/sql tasks the Command field holds the code / query respectively.
	Conn string `json:"conn,omitempty"`
	// Project names an uploaded project directory (under the server's projects
	// dir). When set on a shell task, the scheduler stages a fresh copy of that
	// directory and runs Command with its cwd there (so `python3 main.py` resolves)
	// and CRONOVA_PROJECT_DIR pointing at it. Empty = run with the executor's cwd.
	Project string `json:"project,omitempty"`
}

// HTTPSpec configures an http-type task's request. ExpectedStatus lists the
// status codes counted as success; empty means any 2xx.
type HTTPSpec struct {
	Method         string            `json:"method,omitempty"` // default GET
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	ExpectedStatus []int             `json:"expected_status,omitempty"`
}

// DagRun is one concrete execution of a DAG, keyed by its logical period.
type DagRun struct {
	RunID       string            `json:"run_id"`
	DagID       string            `json:"dag_id"`
	LogicalDate time.Time         `json:"logical_date"`
	State       RunState          `json:"state"`
	TriggerType TriggerType       `json:"trigger_type"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	FinishedAt  *time.Time        `json:"finished_at,omitempty"`
	Params      map[string]string `json:"params,omitempty"` // trigger-time params, injected as CRONOVA_PARAM_* + {{ params.KEY }}
}

// TaskInstance is the execution of one Task within one DagRun. It is the
// smallest unit tracked by the state machine.
type TaskInstance struct {
	ID          int64      `json:"id"`
	RunID       string     `json:"run_id"`
	TaskID      string     `json:"task_id"`
	State       TaskState  `json:"state"`
	TryNumber   int        `json:"try_number"`
	MaxRetries  int        `json:"max_retries"`
	Pool        string     `json:"pool"`
	Priority    int        `json:"priority"`
	ExecutorRef string     `json:"executor_ref,omitempty"`
	LogPath     string     `json:"log_path,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// Pool is a named set of concurrency slots.
type Pool struct {
	Name  string `json:"name"`
	Slots int    `json:"slots"`
}

// DefaultPoolName is the pool tasks land in when none is specified.
const DefaultPoolName = "default"

// Role is a console/API authorization level.
type Role string

const (
	RoleAdmin  Role = "admin"  // full access: trigger, edit, delete
	RoleViewer Role = "viewer" // read-only
)

// User is a console/API account. PasswordHash is a PBKDF2-HMAC-SHA256 hash and is never serialized.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// Variable is a UI-managed shared key-value, referenced as {{ var.Key }}.
type Variable struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Connection is UI-managed structured credentials, referenced as {{ conn.ID.host }}
// etc. Password is stored but NEVER serialized out (write-only, masked in the UI).
type Connection struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Login     string    `json:"login"`
	Password  string    `json:"-"`
	Extra     string    `json:"extra"` // JSON map of extra fields
	UpdatedAt time.Time `json:"updated_at"`
}

// AuditEntry records one operator action (trigger/cancel/retry/mark/create/
// delete/pause) for the operations audit trail. Actor is a username, or
// "anonymous" when auth is disabled.
type AuditEntry struct {
	ID     int64     `json:"id"`
	TS     time.Time `json:"ts"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

// APIToken is a bearer credential for programmatic/machine API access. Only the
// hash is persisted; Plaintext is populated ONLY in the create response (shown
// once). Prefix is the leading chars, kept for display in the token list.
type APIToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Role       Role       `json:"role"`
	Prefix     string     `json:"prefix"`
	Plaintext  string     `json:"token,omitempty"` // create-response only; never stored
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// Session is an opaque server-side session bound to a user.
type Session struct {
	Token     string    `json:"-"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
