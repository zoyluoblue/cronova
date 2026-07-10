package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestVariablesCRUD(t *testing.T) {
	h, _, _, _ := setup(t)
	if rec := do(h, "POST", "/api/variables/db_host", `{"value":"10.0.0.1"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("set var = %d", rec.Code)
	}
	rec := do(h, "GET", "/api/variables", "", nil)
	if !strings.Contains(rec.Body.String(), `"db_host"`) || !strings.Contains(rec.Body.String(), `"10.0.0.1"`) {
		t.Fatalf("list vars missing entry: %s", rec.Body.String())
	}
	// invalid key rejected (@ is URL-valid but not allowed by cfgKeyRe)
	if rec := do(h, "POST", "/api/variables/bad@key", `{"value":"x"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid key = %d, want 400", rec.Code)
	}
	if rec := do(h, "DELETE", "/api/variables/db_host", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete var = %d", rec.Code)
	}
	audit := do(h, "GET", "/api/audit", "", nil).Body.String()
	if !strings.Contains(audit, `"action":"set_variable"`) || !strings.Contains(audit, `"action":"delete_variable"`) {
		t.Fatalf("variable mutations missing from audit: %s", audit)
	}
	if strings.Contains(audit, "10.0.0.1") {
		t.Fatalf("variable value leaked into audit: %s", audit)
	}
}

func TestConnectionPasswordMaskedAndWriteOnly(t *testing.T) {
	h, st, _, _ := setup(t)
	// create with a password
	rec := do(h, "POST", "/api/connections/mysql", `{"type":"mysql","host":"h1","port":3306,"login":"u","password":"s3cret"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("create conn = %d: %s", rec.Code, rec.Body.String())
	}
	// the response must NOT contain the password, but must flag has_password
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("create response leaked password: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"has_password":true`) {
		t.Fatalf("create response missing has_password: %s", rec.Body.String())
	}
	// list also masks
	list := do(h, "GET", "/api/connections", "", nil)
	if strings.Contains(list.Body.String(), "s3cret") {
		t.Fatalf("list leaked password: %s", list.Body.String())
	}
	var got []map[string]any
	_ = json.Unmarshal(list.Body.Bytes(), &got)
	if len(got) != 1 || got[0]["has_password"] != true || got[0]["host"] != "h1" {
		t.Fatalf("unexpected list: %s", list.Body.String())
	}

	// edit WITHOUT a password (empty) must preserve the stored secret
	if rec := do(h, "POST", "/api/connections/mysql", `{"type":"mysql","host":"h2","port":3306,"login":"u"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("edit conn = %d", rec.Code)
	}
	c, err := st.GetConnection(context.Background(), "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "h2" {
		t.Fatalf("host not updated: %q", c.Host)
	}
	if c.Password != "s3cret" {
		t.Fatalf("blank-password edit wiped the secret: %q", c.Password)
	}

	// invalid extra JSON rejected
	if rec := do(h, "POST", "/api/connections/bad", `{"extra":"not json"}`, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid extra = %d, want 400", rec.Code)
	}

	if rec := do(h, "DELETE", "/api/connections/mysql", "", nil); rec.Code != http.StatusOK {
		t.Fatalf("delete conn = %d", rec.Code)
	}
	audit := do(h, "GET", "/api/audit", "", nil).Body.String()
	if !strings.Contains(audit, `"action":"set_connection"`) || !strings.Contains(audit, `"action":"delete_connection"`) {
		t.Fatalf("connection mutations missing from audit: %s", audit)
	}
	if strings.Contains(audit, "s3cret") {
		t.Fatalf("connection password leaked into audit: %s", audit)
	}
}
