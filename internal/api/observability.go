package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/metrics"
	"github.com/zoyluo/cronova/internal/model"
)

// metrics serves Prometheus text-format metrics, all derived from the store at
// scrape time (no in-process counters → no drift, survives restarts). Registered
// on a non-/api/ path so it stays unauthenticated (like /healthz), which scrapers
// expect. It exposes only counts/gauges — never secrets.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var b strings.Builder
	gauge := func(name, help string, val float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", name, help, name, name, strconv.FormatFloat(val, 'g', -1, 64))
	}

	gauge("cronova_up", "1 if the cronova server is up.", 1)
	gauge("cronova_uptime_seconds", "Seconds since the server started.", s.startedSeconds())

	dags, _ := s.store.ListDAGs(ctx)
	paused := 0
	for _, d := range dags {
		if d.Paused {
			paused++
		}
	}
	gauge("cronova_dags_total", "Registered (non-deleted) DAGs.", float64(len(dags)))
	gauge("cronova_dags_paused", "Paused DAGs.", float64(paused))

	active := 0
	for _, st := range []model.RunState{model.RunQueued, model.RunRunning} {
		rs, _ := s.store.ListDagRunsByState(ctx, st)
		active += len(rs)
	}
	gauge("cronova_runs_active", "Queued or running runs.", float64(active))

	// Current rows by state — a gauge, honestly: retention pruning shrinks these
	// counts, which would corrupt rate() if declared a counter (the old
	// cronova_runs_total made exactly that mistake).
	byState, _ := s.store.CountRunsByState(ctx)
	fmt.Fprint(&b, "# HELP cronova_runs_current Runs currently stored, by state (shrinks with retention pruning).\n# TYPE cronova_runs_current gauge\n")
	for _, st := range []model.RunState{model.RunSuccess, model.RunFailed, model.RunTimedOut, model.RunCancelled, model.RunRunning, model.RunQueued} {
		fmt.Fprintf(&b, "cronova_runs_current{state=\"%s\"} %d\n", escapeLabel(string(st)), byState[st])
	}

	// True monotonic counters, maintained in-process at each live→terminal
	// transition (reset on restart — Prometheus handles counter resets).
	fmt.Fprint(&b, "# HELP cronova_runs_finished_total Runs that reached a terminal state since process start.\n# TYPE cronova_runs_finished_total counter\n")
	finished := metrics.RunsFinishedSnapshot()
	for _, st := range []string{"success", "failed", "timed_out", "cancelled"} {
		fmt.Fprintf(&b, "cronova_runs_finished_total{state=\"%s\"} %d\n", escapeLabel(st), finished[st])
	}

	if series := metrics.DurationSnapshot(); len(series) > 0 {
		fmt.Fprint(&b, "# HELP cronova_run_duration_seconds Wall-clock duration of finished runs.\n# TYPE cronova_run_duration_seconds histogram\n")
		for _, ds := range series {
			dag := escapeLabel(ds.DagID)
			for i, ub := range metrics.DurationBuckets {
				fmt.Fprintf(&b, "cronova_run_duration_seconds_bucket{dag_id=\"%s\",le=\"%s\"} %d\n", dag, strconv.FormatFloat(ub, 'g', -1, 64), ds.Cumulative[i])
			}
			fmt.Fprintf(&b, "cronova_run_duration_seconds_bucket{dag_id=\"%s\",le=\"+Inf\"} %d\n", dag, ds.Cumulative[len(ds.Cumulative)-1])
			fmt.Fprintf(&b, "cronova_run_duration_seconds_sum{dag_id=\"%s\"} %s\n", dag, strconv.FormatFloat(ds.SumSec, 'g', -1, 64))
			fmt.Fprintf(&b, "cronova_run_duration_seconds_count{dag_id=\"%s\"} %d\n", dag, ds.Count)
		}
	}

	if lt := metrics.LastTick(); lt > 0 {
		gauge("cronova_scheduler_last_tick_timestamp_seconds", "Unix time of the last completed scheduler tick (stalls indicate a stuck loop).", float64(lt))
	}
	// admission caps: paired with cronova_runs_current, these give the queue
	// watermark (used/cap) so operators see saturation coming, not arriving.
	if s.limitQueued > 0 {
		gauge("cronova_max_queued_runs", "Global queued-run admission cap.", float64(s.limitQueued))
		gauge("cronova_max_active_runs", "Global running-run cap.", float64(s.limitActive))
		gauge("cronova_max_concurrent_tasks", "Global queued/running task cap across all pools.", float64(s.limitTasks))
	}
	fmt.Fprintf(&b, "# HELP cronova_notify_failures_total Webhook notifications that failed to deliver since process start.\n# TYPE cronova_notify_failures_total counter\ncronova_notify_failures_total %d\n", metrics.NotifyFailures())

	if pools, _ := s.store.ListPools(ctx); len(pools) > 0 {
		fmt.Fprint(&b, "# HELP cronova_pool_slots Configured concurrency slots per pool.\n# TYPE cronova_pool_slots gauge\n")
		for _, p := range pools {
			fmt.Fprintf(&b, "cronova_pool_slots{pool=\"%s\"} %d\n", escapeLabel(p.Name), p.Slots)
		}
		fmt.Fprint(&b, "# HELP cronova_pool_used Occupied slots per pool.\n# TYPE cronova_pool_used gauge\n")
		for _, p := range pools {
			used, _ := s.store.CountRunningInPool(ctx, p.Name)
			fmt.Fprintf(&b, "cronova_pool_used{pool=\"%s\"} %d\n", escapeLabel(p.Name), used)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) startedSeconds() float64 {
	if s.started.IsZero() {
		return 0
	}
	return time.Since(s.started).Seconds()
}

// escapeLabel escapes a Prometheus label value (backslash, quote, newline).
func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

// taskDurations — GET /api/dags/{id}/task-durations?limit=N. Recent runs with
// per-task wall-clock durations, newest first: the data behind the console's
// "is this DAG getting slower / which task drags" trend view.
func (s *Server) taskDurations(w http.ResponseWriter, r *http.Request) {
	dagID := r.PathValue("id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	runs, err := s.store.ListDagRuns(r.Context(), dagID, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	type taskDur struct {
		TaskID string `json:"task_id"`
		State  string `json:"state"`
		MS     int64  `json:"ms"`
	}
	type runDur struct {
		RunID       string    `json:"run_id"`
		LogicalDate time.Time `json:"logical_date"`
		State       string    `json:"state"`
		MS          int64     `json:"ms"`
		Tasks       []taskDur `json:"tasks"`
	}
	ms := func(a, b *time.Time) int64 {
		if a == nil || b == nil {
			return 0
		}
		if d := b.Sub(*a).Milliseconds(); d > 0 {
			return d
		}
		return 0
	}
	out := make([]runDur, 0, len(runs))
	for _, run := range runs {
		rd := runDur{RunID: run.RunID, LogicalDate: run.LogicalDate, State: string(run.State), MS: ms(run.StartedAt, run.FinishedAt), Tasks: []taskDur{}}
		tis, err := s.store.ListTaskInstances(r.Context(), run.RunID)
		if err == nil {
			for _, ti := range tis {
				rd.Tasks = append(rd.Tasks, taskDur{TaskID: ti.TaskID, State: string(ti.State), MS: ms(ti.StartedAt, ti.FinishedAt)})
			}
		}
		out = append(out, rd)
	}
	writeJSON(w, http.StatusOK, map[string]any{"dag_id": dagID, "runs": out})
}

// listAudit returns recent audit-trail entries, newest first; ?target=<id>
// filters to one dag/run, ?limit=N caps the count.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	entries, err := s.store.ListAudit(r.Context(), r.URL.Query().Get("target"), limit, offset)
	if err != nil {
		mapErr(w, err)
		return
	}
	if entries == nil {
		entries = []*model.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// audit records an operator action, attributing it to the request's user (or
// "anonymous" when auth is off). Best-effort: a logging failure never fails the
// operation the user just performed.
func (s *Server) audit(r *http.Request, action, target, detail string) {
	actor := "anonymous"
	if u := userFrom(r.Context()); u != nil {
		actor = u.Username
	}
	if err := s.store.RecordAudit(r.Context(), &model.AuditEntry{Actor: actor, Action: action, Target: target, Detail: detail}); err != nil {
		// don't surface — the action succeeded; the audit write is secondary.
		_ = err
	}
}
