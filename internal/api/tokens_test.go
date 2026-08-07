package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
)

// bearer issues a request carrying an Authorization: Bearer token (no cookie).
func bearer(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// createToken logs in as admin (cookie), mints a token, and returns its plaintext.
func mintToken(t *testing.T, h http.Handler, cookie *http.Cookie, name, role string) string {
	t.Helper()
	rec := do(h, "POST", "/api/tokens", `{"name":"`+name+`","role":"`+role+`"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create token: code %d, body %s", rec.Code, rec.Body.String())
	}
	var tok model.APIToken
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.Plaintext == "" {
		t.Fatal("create token response did not include the plaintext")
	}
	return tok.Plaintext
}

func TestBearerTokenAuthenticates(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	cookie := loginCookie(t, h, "u", "pw")
	token := mintToken(t, h, cookie, "ci-bot", "admin")

	// valid admin bearer → protected read works, no cookie
	if rec := bearer(h, "GET", "/api/overview", token); rec.Code != http.StatusOK {
		t.Fatalf("admin bearer read = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// valid admin bearer → write works
	if rec := bearer(h, "POST", "/api/pools/p", token); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("admin bearer write blocked = %d", rec.Code)
	}
	// garbage bearer → 401
	if rec := bearer(h, "GET", "/api/overview", "cnv_pat_not_a_real_token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer = %d, want 401", rec.Code)
	}
	// empty bearer → 401
	if rec := bearer(h, "GET", "/api/overview", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-credential request = %d, want 401", rec.Code)
	}
}

func TestViewerBearerCannotWrite(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	cookie := loginCookie(t, h, "u", "pw")
	viewer := mintToken(t, h, cookie, "readonly", "viewer")

	if rec := bearer(h, "GET", "/api/overview", viewer); rec.Code != http.StatusOK {
		t.Fatalf("viewer bearer read = %d, want 200", rec.Code)
	}
	if rec := bearer(h, "POST", "/api/pools/p", viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer bearer write = %d, want 403", rec.Code)
	}
}

func TestRevokedBearerRejected(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	cookie := loginCookie(t, h, "u", "pw")
	token := mintToken(t, h, cookie, "temp", "admin")

	// find the id, then revoke
	rec := do(h, "GET", "/api/tokens", "", cookie)
	var list []model.APIToken
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("token list len = %d, want 1", len(list))
	}
	for _, tk := range list {
		if tk.Plaintext != "" {
			t.Error("token list leaked a plaintext")
		}
	}
	id := list[0].ID
	if rec := do(h, "DELETE", "/api/tokens/"+strconv.FormatInt(id, 10), "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d, want 200", rec.Code)
	}
	// revoked token no longer authenticates
	if rec := bearer(h, "GET", "/api/overview", token); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked bearer = %d, want 401", rec.Code)
	}
}

// Operator tokens drive runs but cannot touch definitions/config; dag-scoped
// tokens are confined to their DAG; expired tokens stop authenticating.
func TestOperatorScopedAndExpiringTokens(t *testing.T) {
	h := authServer(t, model.RoleAdmin)
	cookie := loginCookie(t, h, "u", "pw")

	// operator: run ops allowed, config writes forbidden, reads allowed
	op := mintToken(t, h, cookie, "ops-bot", "operator")
	if rec := bearer(h, "POST", "/api/dags/etl/trigger", op); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("operator trigger = %d, want allowed", rec.Code)
	}
	if rec := bearer(h, "POST", "/api/runs/etl__r1/cancel", op); rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("operator cancel = %d, want allowed", rec.Code)
	}
	if rec := bearer(h, "POST", "/api/pools/p", op); rec.Code != http.StatusForbidden {
		t.Fatalf("operator pool write = %d, want 403", rec.Code)
	}
	if rec := bearer(h, "GET", "/api/overview", op); rec.Code != http.StatusOK {
		t.Fatalf("operator read = %d, want 200", rec.Code)
	}

	// dag-scoped operator token: only its own DAG's writes pass
	rec := do(h, "POST", "/api/tokens", `{"name":"etl-only","role":"operator","dag_id":"etl"}`, cookie)
	if rec.Code != http.StatusBadRequest { // etl does not exist yet -> rejected
		t.Fatalf("scope to missing dag = %d, want 400", rec.Code)
	}
	// (scope validation against a real DAG is covered in server_test setups)

	// expiring token: a TTL in the past cannot be minted via API (positive only),
	// so mint with a tiny TTL and rely on the expiry check path via store write.
	rec = do(h, "POST", "/api/tokens", `{"name":"short","role":"viewer","ttl":"1ns"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint ttl token = %d body=%s", rec.Code, rec.Body.String())
	}
	var tok model.APIToken
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresAt == nil {
		t.Fatal("ttl token missing expires_at")
	}
	if rec := bearer(h, "GET", "/api/overview", tok.Plaintext); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired token read = %d, want 401", rec.Code)
	}
	// invalid ttl rejected
	if rec := do(h, "POST", "/api/tokens", `{"name":"bad","ttl":"-5h"}`, cookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative ttl = %d, want 400", rec.Code)
	}
}
