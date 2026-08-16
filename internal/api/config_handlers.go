package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

// keys/ids: a conservative identifier so they compose cleanly into {{ var.X }} /
// {{ conn.Y.field }} placeholders (letters, digits, _ . -).
var cfgKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// --- variables ---

func (s *Server) listVariables(w http.ResponseWriter, r *http.Request) {
	vars, err := s.store.ListVariables(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if vars == nil {
		vars = []*model.Variable{}
	}
	writeJSON(w, http.StatusOK, vars)
}

func (s *Server) setVariable(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !cfgKeyRe.MatchString(key) {
		httpErr(w, http.StatusBadRequest, "invalid variable key")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := s.store.UpsertVariable(r.Context(), &model.Variable{Key: key, Value: req.Value}); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "set_variable", key, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteVariable(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteVariable(r.Context(), r.PathValue("key")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "delete_variable", r.PathValue("key"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// --- alert groups ---

// validNotifyFormats mirrors the parser's notify.format vocabulary.
var validNotifyFormats = map[string]bool{"": true, "raw": true, "slack": true, "feishu": true, "dingtalk": true, "email": true}

// validChannelURL enforces the same scheme rules as a DAG's notify.url.
func validChannelURL(u string) bool {
	if u == "" || len(u) > 8192 {
		return false
	}
	lu := strings.ToLower(u)
	return strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "mailto:")
}

func (s *Server) listAlertGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListAlertGroups(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if groups == nil {
		groups = []*model.AlertGroup{}
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) setAlertGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !cfgKeyRe.MatchString(name) || len(name) > 128 {
		httpErrCode(w, http.StatusBadRequest, "bad_group_name", "invalid alert group name")
		return
	}
	var req struct {
		Channels []model.NotifyChannel `json:"channels"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(req.Channels) == 0 || len(req.Channels) > 16 {
		httpErrCode(w, http.StatusBadRequest, "group_channels", "alert group needs 1-16 channels")
		return
	}
	for _, ch := range req.Channels {
		if !validChannelURL(ch.URL) {
			httpErrCode(w, http.StatusBadRequest, "group_channel_url", "channel url must be http(s) or mailto:")
			return
		}
		if !validNotifyFormats[ch.Format] {
			httpErrCode(w, http.StatusBadRequest, "group_channel_format", "invalid channel format")
			return
		}
	}
	if err := s.store.UpsertAlertGroup(r.Context(), &model.AlertGroup{Name: name, Channels: req.Channels}); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "set_alert_group", name, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteAlertGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAlertGroup(r.Context(), r.PathValue("name")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "delete_alert_group", r.PathValue("name"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// --- connections ---

// connResp masks the password: it embeds the connection (whose Password is
// json:"-") and adds a has_password flag so the UI can show ••• without ever
// receiving the secret.
type connResp struct {
	*model.Connection
	HasPassword bool `json:"has_password"`
}

func toConnResp(c *model.Connection) connResp {
	return connResp{Connection: c, HasPassword: c.Password != ""}
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := s.store.ListConnections(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]connResp, 0, len(conns))
	for _, c := range conns {
		out = append(out, toConnResp(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !cfgKeyRe.MatchString(id) {
		httpErr(w, http.StatusBadRequest, "invalid connection id")
		return
	}
	var req struct {
		Type     string `json:"type"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Login    string `json:"login"`
		Password string `json:"password"`
		Extra    string `json:"extra"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Extra != "" && !json.Valid([]byte(req.Extra)) {
		httpErr(w, http.StatusBadRequest, "extra must be valid JSON")
		return
	}
	c := &model.Connection{ID: id, Type: req.Type, Host: req.Host, Port: req.Port, Login: req.Login, Password: req.Password, Extra: req.Extra}
	// write-only password: a blank password on an EXISTING connection preserves
	// the stored secret (the UI never receives it, so it can't echo it back).
	if req.Password == "" {
		if existing, err := s.store.GetConnection(r.Context(), id); err == nil {
			c.Password = existing.Password
		} else if !errors.Is(err, store.ErrNotFound) {
			mapErr(w, err)
			return
		}
	}
	if err := s.store.UpsertConnection(r.Context(), c); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "set_connection", id, "type="+truncate(req.Type, 32))
	writeJSON(w, http.StatusOK, toConnResp(c))
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteConnection(r.Context(), r.PathValue("id")); err != nil {
		mapErr(w, err)
		return
	}
	s.audit(r, "delete_connection", r.PathValue("id"), "")
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
