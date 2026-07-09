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

	// protected without a cookie → 401 (incl. /api/info: it discloses infra metadata)
	for _, p := range []string{"/api/overview", "/api/info"} {
		if rec := do(h, "GET", p, "", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauth %s = %d, want 401", p, rec.Code)
		}
	}
	// only login + health probes are public
	for _, p := range []string{"/healthz", "/readyz"} {
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

func TestCSRFOriginCheck(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	cookie := loginCookie(t, h, "u", "pw")

	post := func(origin, authz string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/pools/p", strings.NewReader(`{"slots":1}`))
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if authz != "" {
			req.Header.Set("Authorization", authz)
		} else {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// httptest.NewRequest sets Host = example.com. A cross-origin cookie write is blocked.
	if rec := post("http://evil.example", ""); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cross-origin") {
		t.Fatalf("cross-origin write = %d %s, want 403 cross-origin", rec.Code, rec.Body.String())
	}
	// Same-origin write passes the CSRF gate (not a 403 cross-origin).
	if rec := post("http://example.com", ""); rec.Code == http.StatusForbidden {
		t.Fatalf("same-origin write blocked: %d %s", rec.Code, rec.Body.String())
	}
	// A request with no Origin/Referer (curl) is allowed — browsers always send one cross-origin.
	if rec := post("", ""); rec.Code == http.StatusForbidden {
		t.Fatalf("origin-less write blocked: %d %s", rec.Code, rec.Body.String())
	}
	// Bearer clients are exempt: a cross-origin token request skips the CSRF gate and
	// fails as 401 (bad token), never 403 cross-origin.
	if rec := post("http://evil.example", "Bearer nope"); rec.Code == http.StatusForbidden {
		t.Fatalf("bearer client should bypass CSRF gate, got 403: %s", rec.Body.String())
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(origin, referer, host string) *http.Request {
		r := httptest.NewRequest("POST", "/x", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if referer != "" {
			r.Header.Set("Referer", referer)
		}
		return r
	}
	if !sameOrigin(mk("http://h:8090", "", "h:8090")) {
		t.Error("matching Origin should be same-origin")
	}
	if sameOrigin(mk("http://evil", "", "h:8090")) {
		t.Error("mismatched Origin should not be same-origin")
	}
	if !sameOrigin(mk("", "http://h:8090/page", "h:8090")) {
		t.Error("matching Referer should be same-origin")
	}
	if !sameOrigin(mk("", "", "h:8090")) {
		t.Error("no Origin/Referer should default to same-origin (non-browser)")
	}
	if isUnsafeMethod(http.MethodGet) || !isUnsafeMethod(http.MethodPost) {
		t.Error("GET safe, POST unsafe")
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
