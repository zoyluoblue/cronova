package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

const workerCols = `worker_id, name, labels, state, draining, version, active_tasks, last_heartbeat, created_at`

func scanWorker(sc scanner) (*model.Worker, error) {
	var w model.Worker
	var labels, created string
	var hb sql.NullString
	var draining int
	if err := sc.Scan(&w.ID, &w.Name, &labels, &w.State, &draining, &w.Version, &w.ActiveTasks, &hb, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	if labels != "" {
		if err := json.Unmarshal([]byte(labels), &w.Labels); err != nil {
			return nil, fmt.Errorf("worker %q: corrupt labels: %w", w.ID, err)
		}
	}
	w.Draining = draining != 0
	w.LastHeartbeat = nsToTime(hb)
	w.CreatedAt = parseLoose(created)
	return &w, nil
}

func (s *Store) UpsertWorker(ctx context.Context, w *model.Worker) error {
	labels, err := json.Marshal(w.Labels)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO workers (worker_id, name, labels, state, draining, version, active_tasks, last_heartbeat, created_at)
		 VALUES (?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
		 ON CONFLICT(worker_id) DO UPDATE SET
		   name=excluded.name, labels=excluded.labels, state=excluded.state,
		   version=excluded.version, active_tasks=excluded.active_tasks,
		   last_heartbeat=excluded.last_heartbeat`,
		w.ID, w.Name, string(labels), w.State, boolInt(w.Draining), w.Version, w.ActiveTasks, fmtNullTime(w.LastHeartbeat))
	return err
}

func (s *Store) GetWorker(ctx context.Context, id string) (*model.Worker, error) {
	return scanWorker(s.db.QueryRowContext(ctx,
		`SELECT `+workerCols+` FROM workers WHERE worker_id=?`, id))
}

func (s *Store) ListWorkers(ctx context.Context) ([]*model.Worker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+workerCols+` FROM workers ORDER BY name, worker_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) UpdateWorkerStatus(ctx context.Context, id, state string, activeTasks int, heartbeat *time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE workers SET state=?, active_tasks=?, last_heartbeat=COALESCE(?, last_heartbeat) WHERE worker_id=?`,
		state, activeTasks, fmtNullTime(heartbeat), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) SetWorkerDraining(ctx context.Context, id string, draining bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE workers SET draining=? WHERE worker_id=?`, boolInt(draining), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWorker(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workers WHERE worker_id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) CreateJoinToken(ctx context.Context, tokenHash, createdBy string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO worker_join_tokens (token_hash, created_by, expires_at) VALUES (?,?,?)`,
		tokenHash, createdBy, fmtTime(expiresAt))
	return err
}

func (s *Store) ConsumeJoinToken(ctx context.Context, tokenHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE worker_join_tokens SET used_at=CURRENT_TIMESTAMP
		 WHERE token_hash=? AND used_at IS NULL AND expires_at > ?`,
		tokenHash, fmtTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound // unknown, expired, or already used
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetDagRunHeld flips a run's operator hold. Terminal runs are refused (a
// hold on a settled run is meaningless and likely a UI race).
func (s *Store) SetDagRunHeld(ctx context.Context, runID string, held bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE dag_runs SET held=? WHERE run_id=? AND state IN ('queued','running')`,
		boolInt(held), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}
