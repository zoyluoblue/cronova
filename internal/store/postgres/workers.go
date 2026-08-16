package postgres

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
	var draining bool
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
	w.Draining = draining
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
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT(worker_id) DO UPDATE SET
		   name=excluded.name, labels=excluded.labels, state=excluded.state,
		   version=excluded.version, active_tasks=excluded.active_tasks,
		   last_heartbeat=excluded.last_heartbeat`,
		w.ID, w.Name, string(labels), w.State, w.Draining, w.Version, w.ActiveTasks,
		fmtNullTime(w.LastHeartbeat), fmtTime(time.Now().UTC()))
	return err
}

func (s *Store) GetWorker(ctx context.Context, id string) (*model.Worker, error) {
	return scanWorker(s.db.QueryRowContext(ctx,
		`SELECT `+workerCols+` FROM workers WHERE worker_id=$1`, id))
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
		`UPDATE workers SET state=$1, active_tasks=$2, last_heartbeat=COALESCE($3, last_heartbeat) WHERE worker_id=$4`,
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
	res, err := s.db.ExecContext(ctx, `UPDATE workers SET draining=$1 WHERE worker_id=$2`, draining, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWorker(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM workers WHERE worker_id=$1`, id)
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
		`INSERT INTO worker_join_tokens (token_hash, created_by, created_at, expires_at) VALUES ($1,$2,$3,$4)`,
		tokenHash, createdBy, fmtTime(time.Now().UTC()), fmtTime(expiresAt))
	return err
}

func (s *Store) ConsumeJoinToken(ctx context.Context, tokenHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE worker_join_tokens SET used_at=$1
		 WHERE token_hash=$2 AND used_at IS NULL AND expires_at > $3`,
		fmtTime(time.Now().UTC()), tokenHash, fmtTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound // unknown, expired, or already used
	}
	return nil
}
