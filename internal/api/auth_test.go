package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store/sqlite"
)

func authServer(t *testing.T, role model.Role) http.Handler {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlite.New(filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("pw")
	if err := st.CreateUser(ctx, &model.User{Username: "u", PasswordHash: hash, Role: role}); err != nil {
		t.Fatal(err)
	}
	srv := New(st, &stubTrigger{}, dir, nil, Info{})
	srv.SetAuth(AuthConfig{Enabled: true, SessionTTL: time.Hour})
	return srv.Handler()
}

func do(h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func loginCookie(t *testing.T, h http.Handler, user, pass string) *http.Cookie {
	t.Helper()
	rec := do(h, "POST", "/api/login", `{"username":"`+user+`","password":"`+pass+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: code %d, body %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("login did not set a session cookie")
	return nil
}

func TestAuthProtectsAPIAndPublicPaths(t *testing.T) {
	h := authServer(t, model.RoleAdmin)

	// protected without a cookie → 401
	if rec := do(h, "GET", "/api/overview", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth overview = %d, want 401", rec.Code)
	}
	// public paths reachable without auth
	for _, p := range []string{"/api/info", "/healthz", "/readyz"} {
		if rec := do(h, "GET", p, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("public %s = %d, want 200", p, rec.Code)
		}
	}
	// bad credentials → 401
	if rec := do(h, "POST", "/api/login", `{"username":"u","password":"nope"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", rec.Code)
	}
	if rec := do(h, "POST", "/api/login", `{"username":"ghost","password":"x"}`, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown-user login = %d, want 401", rec.Code)
	}

	// good credentials → cookie → protected GET works
	cookie := loginCookie(t, h, "u", "pw")
	if rec := do(h, "GET", "/api/overview", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("authed overview = %d, want 200", rec.Code)
	}

	// logout revokes the session
	if rec := do(h, "POST", "/api/logout", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("logout = %d, want 200", rec.Code)
	}
	if rec := do(h, "GET", "/api/overview", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("overview after logout = %d, want 401 (session revoked)", rec.Code)
	}
}

func TestViewerCannotWrite(t *testing.T) {
	h := authServer(t, model.RoleViewer)
	cookie := loginCookie(t, h, "u", "pw")

	// viewer can read
	if rec := do(h, "GET", "/api/overview", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("viewer read = %d, want 200", rec.Code)
	}
	// viewer cannot write → 403 (middleware blocks before the handler)
	if rec := do(h, "POST", "/api/pools/p", `{"slots":1}`, cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer write = %d, want 403", rec.Code)
	}
}

func TestAdminCanWrite(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	cookie := loginCookie(t, h, "u", "pw")
	rec := do(h, "POST", "/api/pools/p", `{"slots":1}`, cookie)
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("admin write blocked = %d", rec.Code)
	}
}
