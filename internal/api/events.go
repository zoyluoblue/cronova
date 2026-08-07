package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

var eventKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)

const maxEventPayloadBytes = 64 << 10

// publishEvent — POST /api/events {key, payload?}. Records an external event;
// DAGs subscribed via trigger_on_event get an event-triggered run on the next
// scheduler tick. (source, key) is idempotent: re-publishing the same key
// cannot double-trigger, so callers can safely retry deliveries.
func (s *Server) publishEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key     string            `json:"key"`
		Payload map[string]string `json:"payload"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" || len(req.Key) > 200 || !eventKeyRe.MatchString(req.Key) {
		httpErr(w, http.StatusBadRequest, "key is required (letters/digits/_.:- only)")
		return
	}
	payload := ""
	if len(req.Payload) > 0 {
		b, err := json.Marshal(req.Payload)
		if err != nil || len(b) > maxEventPayloadBytes {
			httpErr(w, http.StatusBadRequest, "payload must be a small string map")
			return
		}
		payload = string(b)
	}
	created, err := s.store.PublishEvent(r.Context(), model.EventSourceExternal, req.Key, payload)
	if err != nil {
		mapErr(w, err)
		return
	}
	if created {
		s.audit(r, "publish_event", req.Key, "")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "created": created})
}

// --- DAG definition version history ---

type versionStore interface {
	ListDagVersions(ctx context.Context, dagID string, limit int) ([]*model.DagVersion, error)
	GetDagVersion(ctx context.Context, dagID, hash string) (*model.DagVersion, error)
}

// listDagVersions — GET /api/dags/{id}/versions. Newest-first definition
// history; each entry carries its full YAML so the console can diff.
func (s *Server) listDagVersions(w http.ResponseWriter, r *http.Request) {
	vs, ok := s.store.(versionStore)
	if !ok {
		httpErr(w, http.StatusNotImplemented, "store does not keep version history")
		return
	}
	out, err := vs.ListDagVersions(r.Context(), r.PathValue("id"), 20)
	if err != nil {
		mapErr(w, err)
		return
	}
	if out == nil {
		out = []*model.DagVersion{}
	}
	writeJSON(w, http.StatusOK, out)
}

// restoreDagVersion — POST /api/dags/{id}/versions/{hash}/restore. Re-registers
// a historical definition through the normal validated create path (which also
// appends a new version entry — history is never rewritten).
func (s *Server) restoreDagVersion(w http.ResponseWriter, r *http.Request) {
	vs, ok := s.store.(versionStore)
	if !ok {
		httpErr(w, http.StatusNotImplemented, "store does not keep version history")
		return
	}
	v, err := vs.GetDagVersion(r.Context(), r.PathValue("id"), r.PathValue("hash"))
	if err != nil {
		mapErr(w, err)
		return
	}
	dagID, err := s.eng.CreateDAG(r.Context(), v.YAML)
	if err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "restore_dag_version", dagID, v.Hash)
	writeJSON(w, http.StatusOK, map[string]string{"dag_id": dagID, "restored_hash": v.Hash})
}

// --- per-DAG inbound webhook ---
// The hook URL is a capability: whoever has it can trigger THIS one DAG and
// nothing else — the right thing to paste into GitHub Actions or an alerting
// system instead of a bearer token that could administer the whole instance.

// setDagHook — POST /api/dags/{id}/hook. Mints (or rotates) the DAG's hook
// secret and returns the full trigger path once; only a hash is stored.
func (s *Server) setDagHook(w http.ResponseWriter, r *http.Request) {
	dagID := r.PathValue("id")
	if _, err := s.store.GetDAG(r.Context(), dagID); err != nil {
		mapErr(w, err)
		return
	}
	secret, hash, err := auth.NewAPIToken()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "secret generation failed")
		return
	}
	if err := s.store.SetDagHook(r.Context(), dagID, hash, secret[:prefixLen]); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "set_hook", dagID, "")
	writeJSON(w, http.StatusCreated, map[string]string{
		"dag_id": dagID,
		"path":   "/api/hooks/" + dagID + "/" + secret, // shown once, like a token
	})
}

// getDagHook — GET /api/dags/{id}/hook. Reports whether a hook exists (prefix
// + creation time only; the secret is not retrievable).
func (s *Server) getDagHook(w http.ResponseWriter, r *http.Request) {
	_, prefix, created, err := s.store.GetDagHook(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"exists": false})
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exists": true, "prefix": prefix, "created_at": created})
}

// deleteDagHook — DELETE /api/dags/{id}/hook. The hook URL stops working now.
func (s *Server) deleteDagHook(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDagHook(r.Context(), r.PathValue("id")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "delete_hook", r.PathValue("id"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// hookTrigger — POST /api/hooks/{id}/{secret}. The public inbound-webhook
// endpoint: no session or bearer token, the secret in the path IS the
// credential (verified in constant time against its stored hash). Optional
// JSON body {params:{...}} becomes the run's params. Rate-limited per IP and
// per DAG through the same escalating limiter that protects login.
func (s *Server) hookTrigger(w http.ResponseWriter, r *http.Request) {
	dagID, secret := r.PathValue("id"), r.PathValue("secret")
	now := time.Now()
	limKeys := []string{"ip:" + s.clientIP(r), "hook:" + dagID}
	if wait := s.loginLim.retryAfter(now, limKeys...); wait > 0 {
		w.Header().Set("Retry-After", "5")
		httpErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	storedHash, _, _, err := s.store.GetDagHook(r.Context(), dagID)
	// Constant-time compare; run it even when no hook exists so a probe cannot
	// distinguish "no hook" from "wrong secret" by timing.
	calc := auth.HashAPIToken(secret)
	match := err == nil && subtle.ConstantTimeCompare([]byte(calc), []byte(storedHash)) == 1
	if !match {
		s.loginLim.fail(now, limKeys...)
		httpErr(w, http.StatusUnauthorized, "unknown hook")
		return
	}
	s.loginLim.ok(limKeys...)
	var req struct {
		Params map[string]string `json:"params"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		_ = decodeJSON(r, &req) // params are optional; a malformed body just means none
	}
	runID, err := s.eng.TriggerManual(r.Context(), dagID, req.Params)
	if err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "hook_trigger", dagID, runID)
	writeJSON(w, http.StatusOK, map[string]string{"run_id": runID})
}
