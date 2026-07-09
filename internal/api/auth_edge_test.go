package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

func TestMePrincipals(t *testing.T) {
	t.Run("auth disabled reports implicit admin", func(t *testing.T) {
		h, _, _, _ := setup(t)
		rec, body := get(t, h, "GET", "/api/me")
		if rec.Code != 200 {
			t.Fatalf("me = %d", rec.Code)
		}
		m := body.(map[string]any)
		if m["username"] != "" || m["role"] != "admin" || m["auth"] != false {
			t.Errorf("me = %v, want implicit admin with auth:false", m)
		}
	})

	t.Run("session cookie reports the logged-in user", func(t *testing.T) {
		h := authServer(t, model.RoleAdmin)
		cookie := loginCookie(t, h, "u", "pw")
		rec := do(h, "GET", "/api/me", "", cookie)
		if rec.Code != 200 {
			t.Fatalf("me = %d", rec.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m["username"] != "u" || m["role"] != "admin" || m["auth"] != true {
			t.Errorf("me = %v, want u/admin/auth:true", m)
		}
	})

	t.Run("bearer token reports a synthetic token principal", func(t *testing.T) {
		h := authServer(t, model.RoleAdmin)
		cookie := loginCookie(t, h, "u", "pw")
		token := mintToken(t, h, cookie, "bot", "viewer")
		rec := bearer(h, "GET", "/api/me", token)
		if rec.Code != 200 {
			t.Fatalf("me = %d", rec.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m["username"] != "token:bot" || m["role"] != "viewer" || m["auth"] != true {
			t.Errorf("me = %v, want token:bot/viewer/auth:true", m)
		}
	})
}

func TestReadyzReflectsDBHealth(t *testing.T) {
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "ready.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := New(st, &stubTrigger{}, dir, nil, Info{}).Handler()

	rec, body := get(t, h, "GET", "/readyz")
	if rec.Code != 200 || body.(map[string]any)["status"] != "ready" {
		t.Fatalf("healthy readyz = %d %v", rec.Code, body)
	}

	_ = st.Close() // simulate the DB going away
	rec, body = get(t, h, "GET", "/readyz")
	if rec.Code != http.StatusServiceUnavailable || body.(map[string]any)["status"] != "not ready" {
		t.Fatalf("unhealthy readyz = %d %v, want 503 not ready", rec.Code, body)
	}
}

func TestLoginMalformedBody(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	if rec := do(h, "POST", "/api/login", `{"username":`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed login = %d, want 400", rec.Code)
	}
}

func TestLoginThrottledAfterRepeatedFailures(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	for i := 0; i < 3; i++ {
		if rec := do(h, "POST", "/api/login", `{"username":"u","password":"wrong"}`, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed login %d = %d, want 401", i, rec.Code)
		}
	}
	// even the CORRECT password is refused while locked out
	rec := do(h, "POST", "/api/login", `{"username":"u","password":"pw"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked-out login = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

// A failed login is audited with the (truncated) username so an attacker cannot
// stuff unbounded strings into the audit table.
func TestLoginFailureAuditedWithTruncatedUsername(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	long := strings.Repeat("x", 100)
	if rec := do(h, "POST", "/api/login", `{"username":"`+long+`","password":"nope"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", rec.Code)
	}
	cookie := loginCookie(t, h, "u", "pw") // 1 prior IP failure — still under the lockout threshold
	rec := do(h, "GET", "/api/audit", "", cookie)
	if rec.Code != 200 {
		t.Fatalf("audit = %d", rec.Code)
	}
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e["action"] == "login_failed" {
			found = true
			target, _ := e["target"].(string)
			if target != strings.Repeat("x", 64)+"…" {
				t.Errorf("login_failed target = %q, want the username truncated to 64 chars + ellipsis", target)
			}
		}
	}
	if !found {
		t.Error("no login_failed audit entry recorded")
	}
}
