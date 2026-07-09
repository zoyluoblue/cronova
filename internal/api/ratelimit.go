package api

import (
	"net"
	"strings"
	"sync"
	"time"
)

// loginLimiter throttles brute-force login attempts in memory. Failures are
// counted per key (the username and the client IP are tracked as separate
// keys, so one attacker rotating usernames still trips the IP key, and a
// distributed attack on one account trips the username key). The first two
// failures are free; from the third the key locks out for 2s, doubling per
// further failure, capped at 5 minutes. A successful login clears its keys.
//
// State is process-local and lost on restart — acceptable: its job is slowing
// online guessing, not durable banning.
type loginLimiter struct {
	mu sync.Mutex
	m  map[string]*loginFails
}

type loginFails struct {
	fails int
	until time.Time
}

// Per-key-class tuning. The username key throttles online guessing of one
// account but caps at 30s — otherwise an attacker who only knows a username
// could lock its real owner out indefinitely (lockout-as-DoS). The IP key is
// laxer to start (a NAT or reverse proxy funnels many users through one IP)
// but escalates to 5m for a spraying source.
const (
	loginUserFreeFails = 2                // username failures before lockouts start
	loginUserLockMax   = 30 * time.Second // username lockout ceiling (DoS bound)
	loginIPFreeFails   = 10               // IP failures before lockouts start
	loginIPLockMax     = 5 * time.Minute  // IP lockout ceiling
	loginLockBase      = 2 * time.Second  // first lockout, doubles per failure
	loginLimiterSize   = 10_000           // GC threshold: prune expired entries beyond this
)

// keyParams returns (freeFails, lockCap) for a limiter key by its class prefix.
func keyParams(key string) (int, time.Duration) {
	if strings.HasPrefix(key, "ip:") {
		return loginIPFreeFails, loginIPLockMax
	}
	return loginUserFreeFails, loginUserLockMax
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{m: map[string]*loginFails{}} }

// retryAfter returns how long the caller must still wait, or 0 if the attempt
// may proceed. It does not count anything — call fail/ok with the outcome.
func (l *loginLimiter) retryAfter(now time.Time, keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	var wait time.Duration
	for _, k := range keys {
		if f, ok := l.m[k]; ok && now.Before(f.until) {
			if d := f.until.Sub(now); d > wait {
				wait = d
			}
		}
	}
	return wait
}

// fail records a failed attempt and arms the next lockout for each key.
func (l *loginLimiter) fail(now time.Time, keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.m) > loginLimiterSize {
		for k, f := range l.m {
			if now.After(f.until) {
				delete(l.m, k)
			}
		}
	}
	for _, k := range keys {
		f := l.m[k]
		if f == nil {
			f = &loginFails{}
			l.m[k] = f
		}
		f.fails++
		free, cap := keyParams(k)
		if over := f.fails - free; over > 0 {
			if over > 9 {
				over = 9 // 2s << 8 = 512s — already beyond every cap
			}
			lock := loginLockBase << (over - 1)
			if lock > cap {
				lock = cap
			}
			f.until = now.Add(lock)
		}
	}
}

// ok clears the failure state after a successful login — but only for
// username-class keys. The shared IP key keeps its counter: otherwise anyone
// holding one valid account could reset the IP lockout between guesses and
// spray other accounts from the same address without ever escalating.
func (l *loginLimiter) ok(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if !strings.HasPrefix(k, "ip:") {
			delete(l.m, k)
		}
	}
}

// truncate caps a client-supplied string for audit records.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// clientIP extracts the peer IP from RemoteAddr ("host:port"). Deliberately
// NOT X-Forwarded-For: cronova may face the internet directly, and XFF is
// client-controlled there. Behind a reverse proxy all clients share one IP,
// where the per-username key still applies.
func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
