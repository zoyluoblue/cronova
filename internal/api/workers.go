package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/certs"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

// WorkerHubControl is the slice of the worker hub the API needs for live
// session management; the rows themselves live in the store.
type WorkerHubControl interface {
	SetDraining(workerID string, draining bool)
	Disconnect(workerID string)
}

// SetWorkerHub arms the worker endpoints: ca signs join CSRs, hubAddr is the
// advertised gRPC address returned to joining workers, hub controls live
// sessions (nil-safe: without it join is refused, which is the single-binary
// default when worker_listen is not configured).
func (s *Server) SetWorkerHub(ca *certs.CA, hubAddr string, hub WorkerHubControl) {
	s.workerCA, s.workerHubAddr, s.hub = ca, hubAddr, hub
}

var workerNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,64}$`)

// createWorkerToken mints a one-time join token (admin only via withAuth).
// The plaintext is returned exactly once; only its hash is stored.
func (s *Server) createWorkerToken(w http.ResponseWriter, r *http.Request) {
	if s.workerCA == nil {
		httpErrCode(w, http.StatusConflict, "workers_disabled", "worker hub is not enabled (set worker_listen in the server config)")
		return
	}
	var req struct {
		TTL string `json:"ttl"` // Go duration; default 24h
	}
	_ = decodeJSON(r, &req) // empty body = defaults
	ttl := 24 * time.Hour
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil || d <= 0 || d > 30*24*time.Hour {
			httpErr(w, http.StatusBadRequest, "ttl must be a positive duration up to 720h")
			return
		}
		ttl = d
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		mapErr(w, err)
		return
	}
	token := "cwj_" + hex.EncodeToString(raw)
	expires := time.Now().UTC().Add(ttl)
	actor := "anonymous"
	if u := userFrom(r.Context()); u != nil {
		actor = u.Username
	}
	if err := s.store.CreateJoinToken(r.Context(), auth.HashAPIToken(token), actor, expires); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "create_worker_token", "", "")
	writeJSON(w, http.StatusOK, map[string]string{
		"token":      token,
		"expires_at": expires.Format(time.RFC3339),
	})
}

// joinWorker is the PUBLIC bootstrap endpoint: the one-time token is the
// credential (rate-limited like login), the CSR keeps the worker's private
// key on the worker, and the response carries everything a worker needs to
// dial the hub.
func (s *Server) joinWorker(w http.ResponseWriter, r *http.Request) {
	if s.workerCA == nil {
		httpErrCode(w, http.StatusConflict, "workers_disabled", "worker hub is not enabled on this server")
		return
	}
	now := time.Now()
	limKeys := []string{"ip:" + s.clientIP(r), "workers:join"}
	if wait := s.loginLim.retryAfter(now, limKeys...); wait > 0 {
		w.Header().Set("Retry-After", "5")
		httpErr(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req struct {
		Token  string            `json:"token"`
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
		CSRPEM string            `json:"csr_pem"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Token == "" || req.CSRPEM == "" {
		httpErr(w, http.StatusBadRequest, "token and csr_pem are required")
		return
	}
	if !workerNameRe.MatchString(req.Name) {
		httpErr(w, http.StatusBadRequest, "invalid worker name")
		return
	}
	if len(req.Labels) > 16 {
		httpErr(w, http.StatusBadRequest, "too many labels")
		return
	}
	for k, v := range req.Labels {
		if len(k) > 64 || len(v) > 256 {
			httpErr(w, http.StatusBadRequest, "label too long")
			return
		}
	}
	if err := s.store.ConsumeJoinToken(r.Context(), auth.HashAPIToken(req.Token)); err != nil {
		s.loginLim.fail(now, limKeys...)
		if errors.Is(err, store.ErrNotFound) {
			httpErr(w, http.StatusForbidden, "invalid, expired, or already-used join token")
			return
		}
		mapErr(w, err)
		return
	}
	s.loginLim.ok(limKeys...)

	id := make([]byte, 5)
	if _, err := rand.Read(id); err != nil {
		mapErr(w, err)
		return
	}
	workerID := "wk_" + hex.EncodeToString(id)
	certPEM, err := s.workerCA.SignWorkerCSR([]byte(req.CSRPEM), workerID, 365*24*time.Hour)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid CSR")
		return
	}
	if err := s.store.UpsertWorker(r.Context(), &model.Worker{
		ID: workerID, Name: req.Name, Labels: req.Labels, State: model.WorkerOffline,
	}); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "worker_join", workerID, req.Name)
	writeJSON(w, http.StatusOK, map[string]string{
		"worker_id": workerID,
		"cert_pem":  string(certPEM),
		"ca_pem":    string(s.workerCA.CertPEM()),
		"hub_addr":  s.workerHubAddr,
	})
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.store.ListWorkers(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if workers == nil {
		workers = []*model.Worker{}
	}
	writeJSON(w, http.StatusOK, workers)
}

// drainWorker toggles draining: a draining worker finishes its tasks but gets
// no new assignments.
func (s *Server) drainWorker(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Draining bool `json:"draining"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	id := r.PathValue("id")
	if err := s.store.SetWorkerDraining(r.Context(), id, req.Draining); err != nil {
		mapErr(w, err)
		return
	}
	if s.hub != nil {
		s.hub.SetDraining(id, req.Draining)
	}
	s.audit(r, "drain_worker", id, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "draining": req.Draining})
}

// removeWorker deletes the registration and closes any live session. The
// worker's certificate keeps verifying against the CA, but Session rejects
// workers with no store row, so removal is effective immediately.
func (s *Server) removeWorker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteWorker(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	if s.hub != nil {
		s.hub.Disconnect(id)
	}
	s.audit(r, "remove_worker", id, "")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
