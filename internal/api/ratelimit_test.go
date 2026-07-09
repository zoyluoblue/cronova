package api

import (
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()

	// username key: first two failures are free
	l.fail(now, "u:bob")
	l.fail(now, "u:bob")
	if w := l.retryAfter(now, "u:bob"); w != 0 {
		t.Fatalf("after 2 fails wait = %v, want 0", w)
	}
	// third failure locks for the base duration
	l.fail(now, "u:bob")
	if w := l.retryAfter(now, "u:bob"); w != loginLockBase {
		t.Fatalf("after 3 fails wait = %v, want %v", w, loginLockBase)
	}
	// growth doubles…
	l.fail(now, "u:bob")
	if w := l.retryAfter(now, "u:bob"); w != 2*loginLockBase {
		t.Fatalf("after 4 fails wait = %v, want %v", w, 2*loginLockBase)
	}
	// …and the username class caps at 30s (lockout-DoS bound)
	for i := 0; i < 20; i++ {
		l.fail(now, "u:bob")
	}
	if w := l.retryAfter(now, "u:bob"); w != loginUserLockMax {
		t.Fatalf("capped user wait = %v, want %v", w, loginUserLockMax)
	}
	// lock expires with time
	if w := l.retryAfter(now.Add(loginUserLockMax+time.Second), "u:bob"); w != 0 {
		t.Fatalf("expired lock wait = %v, want 0", w)
	}
	// success clears the username key
	l.ok("u:bob", "ip:9.9.9.9")
	l.fail(now, "u:bob")
	l.fail(now, "u:bob")
	if w := l.retryAfter(now, "u:bob"); w != 0 {
		t.Fatalf("after clear + 2 fails wait = %v, want 0", w)
	}

	// IP key: laxer start (free budget), 5m ceiling, and NOT cleared by a
	// success — one valid account must not reset the shared-source lockout.
	for i := 0; i < loginIPFreeFails; i++ {
		l.fail(now, "ip:1.2.3.4")
	}
	if w := l.retryAfter(now, "ip:1.2.3.4"); w != 0 {
		t.Fatalf("IP within free budget should not lock, wait = %v", w)
	}
	l.fail(now, "ip:1.2.3.4") // one over budget
	if w := l.retryAfter(now, "u:carol", "ip:1.2.3.4"); w == 0 {
		t.Fatal("IP key should be locked regardless of username")
	}
	l.ok("u:carol", "ip:1.2.3.4") // success must NOT reset the IP counter
	for i := 0; i < 30; i++ {
		l.fail(now, "ip:1.2.3.4")
	}
	if w := l.retryAfter(now, "ip:1.2.3.4"); w != loginIPLockMax {
		t.Fatalf("IP cap = %v, want %v", w, loginIPLockMax)
	}
}

func TestClientIP(t *testing.T) {
	if ip := clientIP("10.0.0.9:51234"); ip != "10.0.0.9" {
		t.Fatalf("clientIP = %q", ip)
	}
	if ip := clientIP("[::1]:8090"); ip != "::1" {
		t.Fatalf("clientIP v6 = %q", ip)
	}
}
