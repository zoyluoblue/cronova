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
	GetDagRun(ctx context.Context, runID string) (*model.DagRun, error)
	GetDagRunByLogicalDate(ctx context.Context, dagID string, logicalDate time.Time) (*model.DagRun, error)
	ListDagRuns(ctx context.Context, dagID string, limit int) ([]*model.DagRun, error)
	ListDagRunsByState(ctx context.Context, state model.RunState) ([]*model.DagRun, error)
	UpdateDagRunState(ctx context.Context, runID string, state model.RunState, startedAt, finishedAt *time.Time) error
	CountActiveRuns(ctx context.Context, dagID string) (int, error)

	// --- task instances ---
	CreateTaskInstance(ctx context.Context, ti *model.TaskInstance) error
	GetTaskInstance(ctx context.Context, id int64) (*model.TaskInstance, error)
	ListTaskInstances(ctx context.Context, runID string) ([]*model.TaskInstance, error)
	ListTaskInstancesByState(ctx context.Context, state model.TaskState) ([]*model.TaskInstance, error)
	UpdateTaskInstance(ctx context.Context, ti *model.TaskInstance) error

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

	Close() error
}
