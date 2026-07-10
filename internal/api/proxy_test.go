package api

import (
	"net/http/httptest"
	"testing"
)

func TestForwardedHeadersRequireTrustedProxy(t *testing.T) {
	s := &Server{}
	direct := httptest.NewRequest("POST", "http://internal/action", nil)
	direct.RemoteAddr = "198.51.100.10:4321"
	direct.Host = "internal:8090"
	direct.Header.Set("Origin", "https://evil.example")
	direct.Header.Set("X-Forwarded-Host", "evil.example")
	direct.Header.Set("X-Forwarded-For", "203.0.113.9")
	if s.sameOrigin(direct) {
		t.Fatal("direct client spoofed X-Forwarded-Host through the origin check")
	}
	if got := s.clientIP(direct); got != "198.51.100.10" {
		t.Fatalf("direct spoofed client IP = %q", got)
	}

	proxied := httptest.NewRequest("POST", "http://internal/action", nil)
	proxied.RemoteAddr = "127.0.0.1:4321"
	proxied.Host = "internal:8090"
	proxied.Header.Set("Origin", "https://cronova.example")
	proxied.Header.Set("X-Forwarded-Host", "cronova.example")
	proxied.Header.Set("X-Forwarded-For", "203.0.113.9")
	if !s.sameOrigin(proxied) {
		t.Fatal("same-host trusted proxy was not honored")
	}
	if got := s.clientIP(proxied); got != "203.0.113.9" {
		t.Fatalf("proxied client IP = %q", got)
	}
}

func TestSetTrustedProxies(t *testing.T) {
	s := &Server{}
	if err := s.SetTrustedProxies([]string{"10.20.0.0/16", "192.0.2.4"}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "http://internal", nil)
	r.RemoteAddr = "10.20.1.8:1234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.20.2.3")
	if got := s.clientIP(r); got != "198.51.100.7" {
		t.Fatalf("trusted proxy chain client IP = %q", got)
	}
	if err := s.SetTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("invalid trusted proxy was accepted")
	}
}
