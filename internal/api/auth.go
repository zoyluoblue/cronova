package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/model"
)

// AuthConfig controls console/API authentication. Zero value = disabled (open),
// preserving the local-dev experience; production enables it via config.
type AuthConfig struct {
	Enabled      bool
	SessionTTL   time.Duration
	SecureCookie bool // mark the session cookie Secure (set true behind TLS)
}

const sessionCookie = "cnv_session"

// dummyHash equalizes login timing for unknown usernames (anti-enumeration): we
// always run a verify, against this constant when no user matches.
var dummyHash, _ = auth.HashPassword("cronova-dummy-password-for-timing")

type ctxKey int

const userKey ctxKey = iota

func withUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

func userFrom(ctx context.Context) *model.User {
	u, _ := ctx.Value(userKey).(*model.User)
	return u
}

// currentUser resolves the session cookie to a live user, or an error.
func (s *Server) currentUser(r *http.Request) (*model.User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	sess, err := s.store.GetSession(r.Context(), c.Value)
	if err != nil {
		return nil, err
	}
	return s.store.GetUserByID(r.Context(), sess.UserID)
}

// withAuth guards /api/* when auth is enabled. Static assets, /api/info, login,
// and health probes stay public so the login screen can load and probes work.
// Reads (GET) allow any authenticated user; writes require the admin role.
func (s *Server) withAuth(next http.Handler) http.Handler {
	public := map[string]bool{"/api/login": true, "/api/info": true, "/healthz": true, "/readyz": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if public[p] || !strings.HasPrefix(p, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.currentUser(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if r.Method != http.MethodGet && p != "/api/logout" && user.Role != model.RoleAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden: admin role required"})
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure:  s.auth.SecureCookie || r.TLS != nil,
		Expires: time.Now().Add(ttl), MaxAge: int(ttl.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.auth.SecureCookie || r.TLS != nil, MaxAge: -1,
	})
}

// POST /api/login {username, password} → sets session cookie, returns {username, role}.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	user, err := s.store.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		auth.CheckPassword(dummyHash, req.Password) // equalize timing
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session error"})
		return
	}
	ttl := s.auth.SessionTTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if err := s.store.CreateSession(r.Context(), &model.Session{Token: token, UserID: user.ID, ExpiresAt: time.Now().Add(ttl)}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session error"})
		return
	}
	s.setSessionCookie(w, r, token, ttl)
	writeJSON(w, http.StatusOK, map[string]any{"username": user.Username, "role": user.Role})
}

// POST /api/logout — revoke the session and clear the cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.store.DeleteSession(r.Context(), c.Value)
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/me — the current user (401 handled by middleware when unauthenticated).
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u == nil { // auth disabled: report an implicit admin so the console unlocks
		writeJSON(w, http.StatusOK, map[string]any{"username": "", "role": model.RoleAdmin, "auth": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": u.Username, "role": u.Role, "auth": true})
}

// GET /healthz — liveness (process is up).
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

// GET /readyz — readiness (DB reachable).
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if _, err := s.store.CountUsers(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
