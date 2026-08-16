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

// ErrQueueFull is returned when a manual trigger would exceed a configured
// queued-run bound. The API maps it to 429 so clients can retry later.
var ErrQueueFull = errors.New("manual trigger queue is full")

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
	TriggerSubdag     TriggerType = "subdag"   // child run launched by a parent's subdag task
	TriggerBackfill   TriggerType = "backfill" // operator-requested historical re-run
)

// DAG is a workflow definition. Persisted fields live in the dags table; Tasks
// are derived by parsing DefinitionYAML and are not stored row-by-row.
type DAG struct {
	DagID          string    `json:"dag_id"`
	Schedule       string    `json:"schedule"`           // cron expression; empty => manual/event only
	Timezone       string    `json:"timezone,omitempty"` // IANA zone the cron fields evaluate in ("" = UTC)
	StartDate      time.Time `json:"start_date"`
	Catchup        bool      `json:"catchup"`
	Paused         bool      `json:"paused"`
	MaxActiveRuns  int       `json:"max_active_runs"`
	MaxActiveTasks int       `json:"max_active_tasks,omitempty"` // cap on this DAG's concurrent tasks across runs (0 = unlimited)
	// ExecutionPolicy gates queued-run admission: "" (parallel; the default),
	// serial_wait, serial_discard, or serial_priority. Serial policies admit at
	// most one active run regardless of max_active_runs.
	ExecutionPolicy string     `json:"execution_policy,omitempty"`
	DefaultRetries  int        `json:"default_retries"` // DAG-level default; per-task retries override
	DefinitionYAML  string     `json:"definition_yaml,omitempty"`
	Owner           string     `json:"owner,omitempty"`   // reserved for future RBAC
	Project         string     `json:"project,omitempty"` // reserved for future RBAC
	Tasks           []Task     `json:"tasks,omitempty"`
	TriggerAfter    []string   `json:"trigger_after,omitempty"`    // upstream dag_ids
	TriggerOnEvent  []string   `json:"trigger_on_event,omitempty"` // external event keys that trigger a run
	NotifyURL       string     `json:"notify_url,omitempty"`       // webhook (or mailto:) fired on a notify_on state
	NotifyOn        []string   `json:"notify_on,omitempty"`        // run states to notify on: "failure", "success"
	NotifyFormat    string     `json:"notify_format,omitempty"`    // webhook body shape: ""/raw | slack | feishu | dingtalk | email
	NotifyGroup     string     `json:"notify_group,omitempty"`     // alert group name; wins over notify_url when set
	SLA             int        `json:"sla,omitempty"`              // soft deadline (seconds from run start); breach alerts, run keeps going
	DagrunTimeout   int        `json:"dagrun_timeout,omitempty"`   // hard deadline (seconds from run start); breach kills the run → timed_out
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"` // non-nil => soft-deleted (archived)
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
	// WorkerGroup routes this task to a group of dial-in remote workers.
	// Empty = run on the scheduler's configured local/legacy executor.
	WorkerGroup string `json:"worker_group,omitempty"`
	// DependsOnDag holds this task until another DAG's matching period run
	// has succeeded (cross-DAG wait, DS "dependent" semantics).
	DependsOnDag *DependsOnDag `json:"depends_on_dag,omitempty"`
	// Subdag is set when Type == "subdag": the task runs another DAG as a
	// child run and mirrors its terminal state. Cancel cascades; a task retry
	// re-runs the child (RetryRun semantics — history preserved).
	Subdag string `json:"subdag,omitempty"`
	// When is an optional runtime condition template (e.g. "{{ params.env }}"
	// or "{{ ti.check.proceed }}"): evaluated once the task is otherwise ready;
	// a falsy/unresolved render marks the task skipped instead of running it.
	When string `json:"when,omitempty"`
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

// DependsOnDag is a cross-DAG wait condition: the task becomes ready only
// once the target DAG's run for the referenced period is successful.
type DependsOnDag struct {
	Dag string `json:"dag"`
	// Offset shifts which period of the target is awaited, in the datetmpl
	// anchor/offset grammar ("" = same logical date; "- 1d" = the previous
	// day's period; ".month_start" = this month's first period, …).
	Offset    string `json:"offset,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`    // seconds from run start; 0 = wait until dagrun_timeout
	OnTimeout string `json:"on_timeout,omitempty"` // "fail" (default) | "skip"
}

// DagVersion is one entry of a DAG's append-only definition history.
type DagVersion struct {
	ID        int64     `json:"id"`
	DagID     string    `json:"dag_id"`
	Hash      string    `json:"hash"`
	YAML      string    `json:"yaml,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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
	RunID          string            `json:"run_id"`
	DagID          string            `json:"dag_id"`
	LogicalDate    time.Time         `json:"logical_date"`
	State          RunState          `json:"state"`
	TriggerType    TriggerType       `json:"trigger_type"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
	Params         map[string]string `json:"params,omitempty"` // trigger-time params, injected as CRONOVA_PARAM_* + {{ params.KEY }}
	DefinitionYAML string            `json:"-"`                // immutable definition used by this run
	DefinitionHash string            `json:"definition_hash,omitempty"`
	// Priority orders this run against others when competing for dispatch
	// slots and inside a serial_priority queue (higher first; default 0).
	Priority int `json:"priority,omitempty"`
	// ParentRunID links a sub-workflow child run to the parent run whose
	// subdag task launched it ("" = a normal top-level run).
	ParentRunID string `json:"parent_run_id,omitempty"`
}

// Execution policies gate how a DAG's queued runs are admitted when another
// run of the same DAG is already active.
const (
	PolicyParallel       = ""                // (default) max_active_runs applies as configured
	PolicySerialWait     = "serial_wait"     // one at a time; queued runs wait in logical-date order
	PolicySerialDiscard  = "serial_discard"  // one at a time; runs arriving while busy are cancelled
	PolicySerialPriority = "serial_priority" // one at a time; queue drains highest priority first
)

// ValidExecutionPolicy reports whether p names a known execution policy
// ("parallel" is accepted as an alias of the default).
func ValidExecutionPolicy(p string) bool {
	switch p {
	case PolicyParallel, "parallel", PolicySerialWait, PolicySerialDiscard, PolicySerialPriority:
		return true
	}
	return false
}

// Event is a durable scheduler signal. The dependency source uses a DagRun's
// run_id as EventKey so finalizing the run and publishing its success can be
// committed atomically and replayed safely after a restart.
type Event struct {
	ID        int64     `json:"id"`
	Source    string    `json:"source"`
	EventKey  string    `json:"event_key"`
	Payload   string    `json:"payload,omitempty"`
	Consumed  bool      `json:"consumed"`
	CreatedAt time.Time `json:"created_at"`
}

const EventSourceDependency = "dependency"

// EventSourceExternal marks events published from outside (POST /api/events or
// an inbound webhook): DAGs subscribe via trigger_on_event and the scheduler
// creates an event-triggered run per subscriber. event_key is the idempotency
// key — publishing the same key twice cannot double-trigger.
const EventSourceExternal = "external"

// TaskInstance is the execution of one Task within one DagRun. It is the
// smallest unit tracked by the state machine.
type TaskInstance struct {
	ID             int64      `json:"id"`
	RunID          string     `json:"run_id"`
	TaskID         string     `json:"task_id"`
	State          TaskState  `json:"state"`
	TryNumber      int        `json:"try_number"`
	MaxRetries     int        `json:"max_retries"`
	Pool           string     `json:"pool"`
	Priority       int        `json:"priority"`
	DefinitionHash string     `json:"definition_hash,omitempty"`
	ExecutorRef    string     `json:"executor_ref,omitempty"`
	LogPath        string     `json:"log_path,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// Pool is a named set of concurrency slots.
type Pool struct {
	Name  string `json:"name"`
	Slots int    `json:"slots"`
}

// DefaultPoolName is the pool tasks land in when none is specified.
const DefaultPoolName = "default"

// MaxPoolSlots is the largest operator-configurable pool. A scheduler-wide
// concurrency cap remains the final bound even when many pools exist.
const MaxPoolSlots = 1024

// Role is a console/API authorization level.
type Role string

const (
	RoleAdmin Role = "admin" // full access: trigger, edit, delete
	// RoleOperator can run things (trigger, cancel, retry, backfill, pause,
	// mark) but cannot change definitions or configuration — the right grant
	// for CI pipelines and ops automations that only need to drive runs.
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer" // read-only
)

// User is a console/API account. PasswordHash is a PBKDF2-HMAC-SHA256 hash and is never serialized.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
	// TokenDagScope carries a dag-scoped API token's restriction through the
	// request principal (never persisted or serialized): when non-empty, write
	// operations are limited to this DAG.
	TokenDagScope string `json:"-"`
}

// Worker states. A worker is online while its Session stream is up and
// heartbeating; offline after a clean disconnect; lost when heartbeats stopped
// without a goodbye (its running tasks are failed over per retry policy).
const (
	WorkerOnline  = "online"
	WorkerOffline = "offline"
	WorkerLost    = "lost"
)

// Worker is a remote task runner that joined the cluster via a join token.
// Labels carry routing metadata; the "group" label (default "default") is the
// worker group tasks target with worker_group:.
type Worker struct {
	ID            string            `json:"worker_id"`
	Name          string            `json:"name"`
	Labels        map[string]string `json:"labels,omitempty"`
	State         string            `json:"state"`
	Draining      bool              `json:"draining,omitempty"` // no new assignments; running tasks finish
	Version       string            `json:"version,omitempty"`
	ActiveTasks   int               `json:"active_tasks"`
	LastHeartbeat *time.Time        `json:"last_heartbeat,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// Group returns the worker's routing group (the "group" label, default
// "default").
func (w *Worker) Group() string {
	if g := w.Labels["group"]; g != "" {
		return g
	}
	return "default"
}

// NotifyChannel is one delivery target inside an alert group: an http(s)
// webhook or a mailto: address list, with the same format vocabulary as a
// DAG's notify.format ("" / raw | slack | feishu | dingtalk | email).
type NotifyChannel struct {
	URL    string `json:"url"`
	Format string `json:"format,omitempty"`
}

// AlertGroup is a named fan-out of notify channels. A DAG referencing the
// group (notify.group) alerts every channel; groups keep channel lists in one
// place instead of one webhook URL pasted into every DAG.
type AlertGroup struct {
	Name      string          `json:"name"`
	Channels  []NotifyChannel `json:"channels"`
	UpdatedAt time.Time       `json:"updated_at"`
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

// AuditEntry records one operator mutation for the operations audit trail.
// Actor is a username, or "anonymous" when auth is disabled. Detail must never
// contain credentials or variable/connection values.
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
	// ExpiresAt makes the token stop authenticating after this instant
	// (nil = never — the pre-existing behavior). Supports rotation policies.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// DagID scopes the token's WRITE operations to one DAG (trigger/backfill/
	// pause and that DAG's run ops). "" = unscoped. Reads stay role-governed.
	DagID string `json:"dag_id,omitempty"`
}

// Session is an opaque server-side session bound to a user.
type Session struct {
	Token     string    `json:"-"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
