package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/model"
)

// prefixLen is how many leading chars of a token we keep for display (enough to
// recognize a token in the list without revealing anything usable).
const prefixLen = len(auth.APITokenPrefix) + 6

// listTokens — GET /api/tokens. Returns metadata only (never hash/plaintext).
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if tokens == nil {
		tokens = []*model.APIToken{}
	}
	writeJSON(w, http.StatusOK, tokens)
}

// createToken — POST /api/tokens {name, role}. Mints a bearer token, stores only
// its hash, and returns the plaintext ONCE (never retrievable again). Admin-only
// (enforced by withAuth for non-GET). role defaults to "admin"; only admin/viewer
// are accepted.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httpErr(w, http.StatusBadRequest, "name is required")
		return
	}
	role := model.Role(req.Role)
	if role == "" {
		role = model.RoleAdmin
	}
	if role != model.RoleAdmin && role != model.RoleViewer {
		httpErr(w, http.StatusBadRequest, "role must be admin or viewer")
		return
	}
	plaintext, hash, err := auth.NewAPIToken()
	if err != nil {
		httpErr(w, http.StatusInternalServerError, "token generation failed")
		return
	}
	tok := &model.APIToken{Name: req.Name, Role: role, Prefix: plaintext[:prefixLen]}
	if err := s.store.CreateAPIToken(r.Context(), tok, hash); err != nil {
		mapErr(w, err)
		return
	}
	tok.Plaintext = plaintext // returned once, in this response only
	s.audit(r, "create_token", req.Name, string(role))
	writeJSON(w, http.StatusCreated, tok)
}

// deleteToken — DELETE /api/tokens/{id}. Revokes a token immediately.
func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpErr(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if err := s.store.DeleteAPIToken(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "delete_token", strconv.FormatInt(id, 10), "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
