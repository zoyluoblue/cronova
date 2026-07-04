package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	byState, _ := s.store.CountRunsByState(ctx)
	fmt.Fprint(&b, "# HELP cronova_runs_total Total runs by final state (all time).\n# TYPE cronova_runs_total counter\n")
	for _, st := range []model.RunState{model.RunSuccess, model.RunFailed, model.RunTimedOut, model.RunCancelled, model.RunRunning, model.RunQueued} {
		fmt.Fprintf(&b, "cronova_runs_total{state=\"%s\"} %d\n", escapeLabel(string(st)), byState[st])
	}

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

// listAudit returns recent audit-trail entries, newest first; ?target=<id>
// filters to one dag/run, ?limit=N caps the count.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.store.ListAudit(r.Context(), r.URL.Query().Get("target"), limit)
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
