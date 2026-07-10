package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestParseBool locks in the fail-safe contract: recognized truthy/falsy tokens
// parse as expected (case-insensitive, trimmed), and anything unrecognized is
// reported invalid so callers keep their secure default instead of failing open.
func TestParseBool(t *testing.T) {
	cases := []struct {
		in         string
		val, valid bool
	}{
		{"1", true, true}, {"true", true, true}, {"yes", true, true},
		{"on", true, true}, {"y", true, true}, {"enabled", true, true},
		{"TRUE", true, true}, {"True", true, true}, {"  true  ", true, true},
		{"0", false, true}, {"false", false, true}, {"no", false, true},
		{"off", false, true}, {"n", false, true}, {"disabled", false, true},
		{"FALSE", false, true},
		// unrecognized / blank -> invalid, so the caller keeps its default
		{"", false, false}, {"maybe", false, false}, {"2", false, false},
		{"tru", false, false}, {"enable-auth", false, false},
	}
	for _, c := range cases {
		val, valid := parseBool(c.in)
		if val != c.val || valid != c.valid {
			t.Errorf("parseBool(%q) = (%v,%v), want (%v,%v)", c.in, val, valid, c.val, c.valid)
		}
	}
}

func TestDefaultHTTPExposureIsLoopbackOnly(t *testing.T) {
	c := defaultConfig()
	if c.HTTP != "127.0.0.1:8090" {
		t.Fatalf("default HTTP = %q, want loopback", c.HTTP)
	}
	if err := validateHTTPExposure(c); err != nil {
		t.Fatalf("safe default rejected: %v", err)
	}
}

func TestValidateHTTPExposure(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8090", "localhost:8090", "[::1]:8090"} {
		c := defaultConfig()
		c.HTTP = addr
		if err := validateHTTPExposure(c); err != nil {
			t.Errorf("loopback address %q rejected: %v", addr, err)
		}
	}

	c := defaultConfig()
	c.HTTP = ":8090"
	if err := validateHTTPExposure(c); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("unauthenticated wildcard bind error = %v, want refusal", err)
	}
	c.Auth.Enabled = true
	if err := validateHTTPExposure(c); err != nil {
		t.Fatalf("authenticated wildcard bind rejected: %v", err)
	}
	c.Auth.Enabled = false
	c.AllowUnauthenticatedRemote = true
	if err := validateHTTPExposure(c); err != nil {
		t.Fatalf("explicit remote override rejected: %v", err)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cronova.yaml")
	if err := os.WriteFile(path, []byte("http: 127.0.0.1:8090\nhtpt: typo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := defaultConfig()
	err := loadConfigFile(&c, path, true)
	if err == nil || !strings.Contains(err.Error(), "field htpt not found") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestConsoleHTTPServerHasResourceTimeouts(t *testing.T) {
	srv := newConsoleHTTPServer("127.0.0.1:0", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if srv.ReadHeaderTimeout != 5*time.Second || srv.ReadTimeout != 30*time.Second || srv.WriteTimeout != 60*time.Second || srv.IdleTimeout != 60*time.Second {
		t.Fatalf("unexpected timeouts: header=%s read=%s write=%s idle=%s", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, 1<<20)
	}
}
