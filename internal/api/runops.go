package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// Plan2 R5B run-operations surface: hold/release intents and the read-only
// decision timeline. Hold pauses NEW dispatch only (running tasks continue);
// the timeline is a chronological projection over existing authoritative
// records (run states, task attempts, audit entries) — no second ledger.

func (s *Server) holdRun(w http.ResponseWriter, r *http.Request) {
	s.setRunHold(w, r, true, "hold_run")
}

func (s *Server) releaseRun(w http.ResponseWriter, r *http.Request) {
	s.setRunHold(w, r, false, "release_run")
}

func (s *Server) setRunHold(w http.ResponseWriter, r *http.Request, held bool, action string) {
	runID := r.PathValue("runID")
	if err := s.store.SetDagRunHeld(r.Context(), runID, held); err != nil {
		// not-found doubles as "run already terminal" (the guarded UPDATE
		// matched no active row) — either way there is nothing to hold.
		httpErrCode(w, http.StatusConflict, "run_not_active", "run not found or already finished")
		return
	}
	s.audit(r, action, runID, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "held": held})
}

// timelineEvent is one row of the run's decision/execution history.
type timelineEvent struct {
	At     string `json:"at"`
	Kind   string `json:"kind"` // run | task | audit
	Event  string `json:"event"`
	TaskID string `json:"task_id,omitempty"`
	Try    int    `json:"try,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// runTimeline merges run lifecycle, task attempts, and operator actions into
// one chronological view (bounded; newest data wins no rewriting).
func (s *Server) runTimeline(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	run, err := s.store.GetDagRun(r.Context(), runID)
	if err != nil {
		mapErr(w, err)
		return
	}
	var ev []timelineEvent
	add := func(at *time.Time, kind, event, taskID string, try int, actor, detail string) {
		if at == nil || at.IsZero() {
			return
		}
		ev = append(ev, timelineEvent{At: at.UTC().Format(time.RFC3339), Kind: kind, Event: event, TaskID: taskID, Try: try, Actor: actor, Detail: detail})
	}
	ld := run.LogicalDate
	add(&ld, "run", "created (logical "+run.LogicalDate.UTC().Format(time.RFC3339)+", "+string(run.TriggerType)+")", "", 0, "", "")
	add(run.StartedAt, "run", "started", "", 0, "", "")
	if run.FinishedAt != nil {
		add(run.FinishedAt, "run", string(run.State), "", 0, "", "")
	}

	if tis, err := s.store.ListTaskInstances(r.Context(), runID); err == nil {
		for _, ti := range tis {
			add(ti.StartedAt, "task", "started", ti.TaskID, ti.TryNumber, "", "")
			if ti.FinishedAt != nil {
				add(ti.FinishedAt, "task", string(ti.State), ti.TaskID, ti.TryNumber, "", "")
			}
		}
	}
	// Operator actions targeting this run (cancel/retry/mark/hold/release).
	if entries, err := s.store.ListAudit(r.Context(), runID, 200, 0); err == nil {
		for _, a := range entries {
			ts := a.TS
			add(&ts, "audit", a.Action, "", 0, a.Actor, a.Detail)
		}
	}
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At < ev[j].At })
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": runID, "state": run.State, "held": run.Held, "events": ev,
	})
}

var _ = model.RunQueued // keep model import stable if fields shift
