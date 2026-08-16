package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

// AcquireLease claims the single scheduler lease for holder, valid for ttl.
// It succeeds when the lease is absent, expired (a crashed holder), or already
// ours; otherwise it returns store.ErrLeaseHeld wrapped with the current
// holder — the caller should refuse to start scheduling.
func (s *Store) AcquireLease(ctx context.Context, holder string, ttl time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	var cur string
	var expStr string
	// FOR UPDATE: the SQLite store got check-then-take atomicity for free from
	// its single serialized connection; with Postgres two starting instances can
	// run this transaction concurrently, so the row lock is what prevents both
	// from concluding the lease is stale and taking it.
	err = tx.QueryRowContext(ctx, `SELECT holder, expires_at FROM scheduler_lease WHERE id = 1 FOR UPDATE`).Scan(&cur, &expStr)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Two first-ever starters can both see "no row"; ON CONFLICT DO NOTHING
		// lets the loser detect the race instead of failing with a unique error.
		res, err := tx.ExecContext(ctx,
			`INSERT INTO scheduler_lease (id, holder, expires_at) VALUES (1, $1, $2) ON CONFLICT (id) DO NOTHING`,
			holder, now.Add(ttl).Format(timeLayout))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: another instance acquired it concurrently", store.ErrLeaseHeld)
		}
	case err != nil:
		return err
	default:
		exp, perr := time.Parse(timeLayout, expStr)
		if perr == nil && cur != holder && exp.After(now) {
			return fmt.Errorf("%w: held by %q until %s", store.ErrLeaseHeld, cur, exp.Format(time.RFC3339))
		}
		// expired, unparsable (treat as stale), or already ours — take it over.
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_lease SET holder = $1, expires_at = $2 WHERE id = 1`,
			holder, now.Add(ttl).Format(timeLayout)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RenewLease extends the lease iff holder still owns it. Returns
// store.ErrLeaseLost when another instance has taken over (the caller must
// stop scheduling immediately).
func (s *Store) RenewLease(ctx context.Context, holder string, ttl time.Duration) error {
	res, err := s.db.ExecContext(ctx, `UPDATE scheduler_lease SET expires_at = $1 WHERE id = 1 AND holder = $2`,
		time.Now().UTC().Add(ttl).Format(timeLayout), holder)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrLeaseLost
	}
	return nil
}

// ReleaseLease drops the lease if holder still owns it (clean shutdown).
func (s *Store) ReleaseLease(ctx context.Context, holder string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduler_lease WHERE id = 1 AND holder = $1`, holder)
	return err
}

// SetTaskOutput stores (replaces) a task's emitted output for its run.
func (s *Store) SetTaskOutput(ctx context.Context, runID, taskID, output string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO task_outputs(run_id, task_id, output) VALUES ($1,$2,$3)
ON CONFLICT(run_id, task_id) DO UPDATE SET output=excluded.output`, runID, taskID, output)
	return err
}

// GetTaskOutput returns a task's emitted output JSON, or ErrNotFound.
func (s *Store) GetTaskOutput(ctx context.Context, runID, taskID string) (string, error) {
	var out string
	err := s.db.QueryRowContext(ctx, `SELECT output FROM task_outputs WHERE run_id=$1 AND task_id=$2`, runID, taskID).Scan(&out)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return out, err
}

// --- tick-path aggregate queries (one GROUP BY instead of N point queries) ---

// ListDagRunsByStateLimited is ListDagRunsByState with a LIMIT: the scheduler
// caps how much queued backlog one tick materializes instead of always paging
// the full 10k admission window through memory.
func (s *Store) ListDagRunsByStateLimited(ctx context.Context, state model.RunState, limit int) ([]*model.DagRun, error) {
	if limit <= 0 {
		return s.ListDagRunsByState(ctx, state)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runCols+` FROM dag_runs WHERE state=$1 ORDER BY logical_date LIMIT $2`, string(state), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DagRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountActiveRunsByDag returns queued+running run counts for every DAG in one
// query (createDueRuns previously issued one COUNT per DAG per tick).
func (s *Store) CountActiveRunsByDag(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dag_id, COUNT(*) FROM dag_runs WHERE state IN ('queued','running') GROUP BY dag_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// --- DAG definition version history ---

// AppendDagVersion records a definition change; a hash equal to the latest
// stored version is a no-op (restarts and reloads re-register unchanged DAGs).
func (s *Store) AppendDagVersion(ctx context.Context, dagID, hash, yaml string) error {
	var last string
	err := s.db.QueryRowContext(ctx,
		`SELECT hash FROM dag_versions WHERE dag_id=$1 ORDER BY id DESC LIMIT 1`, dagID).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if last == hash {
		return nil
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dag_versions(dag_id, hash, yaml) VALUES ($1,$2,$3)`, dagID, hash, yaml)
	return err
}

// ListDagVersions returns a DAG's definition history, newest first.
func (s *Store) ListDagVersions(ctx context.Context, dagID string, limit int) ([]*model.DagVersion, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, dag_id, hash, yaml, ts FROM dag_versions WHERE dag_id=$1 ORDER BY id DESC LIMIT $2`, dagID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DagVersion
	for rows.Next() {
		v := &model.DagVersion{}
		var ts string
		if err := rows.Scan(&v.ID, &v.DagID, &v.Hash, &v.YAML, &ts); err != nil {
			return nil, err
		}
		v.CreatedAt = parseLoose(ts)
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetDagVersion returns one stored definition by (dag, hash), or ErrNotFound.
func (s *Store) GetDagVersion(ctx context.Context, dagID, hash string) (*model.DagVersion, error) {
	v := &model.DagVersion{}
	var ts string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, dag_id, hash, yaml, ts FROM dag_versions WHERE dag_id=$1 AND hash=$2 ORDER BY id DESC LIMIT 1`, dagID, hash).
		Scan(&v.ID, &v.DagID, &v.Hash, &v.YAML, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	v.CreatedAt = parseLoose(ts)
	return v, nil
}

// CountActiveTasksForDag counts a DAG's queued+running task instances across
// ALL of its runs — the denominator for max_active_tasks.
func (s *Store) CountActiveTasksForDag(ctx context.Context, dagID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM task_instances ti
JOIN dag_runs r ON r.run_id = ti.run_id
WHERE r.dag_id = $1 AND ti.state IN ('queued','running') AND ti.executor_ref NOT LIKE 'subdag:%'`, dagID).Scan(&n)
	return n, err
}
