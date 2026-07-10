// Package store defines the persistence interface for cronova. The interface
// isolates scheduling logic from the storage engine: the v1 implementation is
// SQLite (see store/sqlite), and a future PostgreSQL implementation can be
// dropped in without touching callers.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrAlreadyExists is returned when creating a row that violates a uniqueness
// constraint (notably a DagRun for an already-existing (dag_id, logical_date)).
// Callers performing catchup rely on this to skip already-created runs.
var ErrAlreadyExists = errors.New("store: already exists")

// Store is the full persistence surface used by the scheduler, API, and
// recovery modules.
type Store interface {
	// Migrate creates the schema (idempotent) and seeds the default pool.
	Migrate(ctx context.Context) error

	// --- DAGs ---
	UpsertDAG(ctx context.Context, d *model.DAG) error
	GetDAG(ctx context.Context, dagID string) (*model.DAG, error)
	ListDAGs(ctx context.Context) ([]*model.DAG, error)
	SetDAGPaused(ctx context.Context, dagID string, paused bool) error
	// SoftDeleteDAG archives a DAG (sets deleted_at), hiding it from lists while
	// preserving its row and run history. Returns ErrNotFound if absent/already deleted.
	SoftDeleteDAG(ctx context.Context, dagID string) error

	// --- DAG runs ---
	// CreateDagRun inserts a run. It returns ErrAlreadyExists if a run for the
	// same (dag_id, logical_date) already exists.
	CreateDagRun(ctx context.Context, r *model.DagRun) error
	// CreateManualDagRunBounded atomically inserts a manual run only while both
	// active-run bounds have capacity. It returns model.ErrQueueFull otherwise.
	CreateManualDagRunBounded(ctx context.Context, r *model.DagRun, perDAG, global int) error
	GetDagRun(ctx context.Context, runID string) (*model.DagRun, error)
	GetDagRunByLogicalDate(ctx context.Context, dagID string, logicalDate time.Time) (*model.DagRun, error)
	ListDagRuns(ctx context.Context, dagID string, limit int) ([]*model.DagRun, error)
	// ListDagRunsPage is ListDagRuns with optional state filtering and offset
	// paging (states nil/empty = all states).
	ListDagRunsPage(ctx context.Context, dagID string, states []model.RunState, limit, offset int) ([]*model.DagRun, error)
	// LatestScheduledRun returns the schedule-triggered run with the greatest
	// logical_date, or ErrNotFound. It is the scheduler's catchup anchor, so it
	// must be immune to how many manual/backfill runs exist.
	LatestScheduledRun(ctx context.Context, dagID string) (*model.DagRun, error)
	ListDagRunsByState(ctx context.Context, state model.RunState) ([]*model.DagRun, error)
	// RecentRuns returns the most recent runs across all live DAGs, newest first.
	RecentRuns(ctx context.Context, limit int) ([]*model.DagRun, error)
	UpdateDagRunState(ctx context.Context, runID string, state model.RunState, startedAt, finishedAt *time.Time) error
	CountActiveRuns(ctx context.Context, dagID string) (int, error)
	// CountRunsByState returns the all-time run count grouped by state (for /metrics).
	CountRunsByState(ctx context.Context) (map[model.RunState]int, error)
	// PruneRuns deletes finished runs (and their task instances) whose
	// finished_at is before cutoff, returning the deleted runs (dag_id + run_id
	// populated) so callers can remove the runs' log directories. Runs that are
	// still queued/running — or finished runs with no finished_at — are never touched.
	PruneRuns(ctx context.Context, cutoff time.Time) ([]*model.DagRun, error)

	// --- audit log ---
	RecordAudit(ctx context.Context, e *model.AuditEntry) error
	// ListAudit returns audit entries newest-first; target != "" filters to one dag/run.
	ListAudit(ctx context.Context, target string, limit int) ([]*model.AuditEntry, error)

	// --- API tokens (Bearer auth) ---
	CreateAPIToken(ctx context.Context, t *model.APIToken, hash string) error
	ListAPITokens(ctx context.Context) ([]*model.APIToken, error)
	// GetAPITokenByHash resolves an incoming bearer token's hash to its record.
	GetAPITokenByHash(ctx context.Context, hash string) (*model.APIToken, error)
	TouchAPIToken(ctx context.Context, id int64) error
	DeleteAPIToken(ctx context.Context, id int64) error

	// --- task instances ---
	CreateTaskInstance(ctx context.Context, ti *model.TaskInstance) error
	GetTaskInstance(ctx context.Context, id int64) (*model.TaskInstance, error)
	ListTaskInstances(ctx context.Context, runID string) ([]*model.TaskInstance, error)
	ListTaskInstancesByState(ctx context.Context, state model.TaskState) ([]*model.TaskInstance, error)
	UpdateTaskInstance(ctx context.Context, ti *model.TaskInstance) error
	// UpdateTaskInstanceGuarded is an optimistic CAS: it applies only if the row
	// still has expectRef and is non-terminal. Returns whether it applied.
	UpdateTaskInstanceGuarded(ctx context.Context, ti *model.TaskInstance, expectRef string) (bool, error)

	// --- cross-DAG dependencies (dependency trigger) ---
	// ReplaceDagDependencies sets downstream's upstream list to exactly upstreams.
	ReplaceDagDependencies(ctx context.Context, downstream string, upstreams []string) error
	// ListDownstreams returns dag_ids that depend on (run after) upstream.
	ListDownstreams(ctx context.Context, upstream string) ([]string, error)
	// ListUpstreams returns the dag_ids downstream depends on.
	ListUpstreams(ctx context.Context, downstream string) ([]string, error)

	// --- pools ---
	UpsertPool(ctx context.Context, p *model.Pool) error
	GetPool(ctx context.Context, name string) (*model.Pool, error)
	ListPools(ctx context.Context) ([]*model.Pool, error)
	// CountRunningInPool returns how many task instances currently occupy a
	// slot in the named pool (states queued + running).
	CountRunningInPool(ctx context.Context, pool string) (int, error)

	// --- auth: users + sessions ---
	CreateUser(ctx context.Context, u *model.User) error
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	GetUserByID(ctx context.Context, id int64) (*model.User, error)
	ListUsers(ctx context.Context) ([]*model.User, error)
	CountUsers(ctx context.Context) (int, error)
	// UpdateUserPassword sets a new PBKDF2-HMAC-SHA256 hash and revokes the user's sessions.
	UpdateUserPassword(ctx context.Context, id int64, passwordHash string) error
	DeleteUser(ctx context.Context, id int64) error
	CreateSession(ctx context.Context, s *model.Session) error
	// GetSession returns the session only if it exists and has not expired.
	GetSession(ctx context.Context, token string) (*model.Session, error)
	DeleteSession(ctx context.Context, token string) error
	// DeleteExpiredSessions prunes sessions past their expiry.
	DeleteExpiredSessions(ctx context.Context) error

	// --- UI-managed config: variables + connections ---
	ListVariables(ctx context.Context) ([]*model.Variable, error)
	GetVariable(ctx context.Context, key string) (*model.Variable, error)
	UpsertVariable(ctx context.Context, v *model.Variable) error
	DeleteVariable(ctx context.Context, key string) error
	ListConnections(ctx context.Context) ([]*model.Connection, error)
	GetConnection(ctx context.Context, id string) (*model.Connection, error)
	UpsertConnection(ctx context.Context, c *model.Connection) error
	DeleteConnection(ctx context.Context, id string) error

	Close() error
}
