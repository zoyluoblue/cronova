package main

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config mirrors the `serve` settings. Precedence (highest first):
// explicit flag > CRONOVA_* env > config file > built-in default.
type Config struct {
	DB         string `yaml:"db"`
	Dags       string `yaml:"dags"`
	Logs       string `yaml:"logs"`
	Projects   string `yaml:"projects"`   // dir of uploaded project files ("" = ~/.cronova/projects)
	Workspaces string `yaml:"workspaces"` // shared per-attempt project copies
	Tick       string `yaml:"tick"`
	Executor   string `yaml:"executor"`
	HTTP       string `yaml:"http"`
	// Reload re-scans the dags directory for changed YAML this often (GitOps:
	// a git pull goes live without restart). "0"/empty = off.
	Reload string `yaml:"reload"`
	// AllowUnauthenticatedRemote is an explicit escape hatch for binding the
	// unauthenticated console to a non-loopback address.
	AllowUnauthenticatedRemote bool `yaml:"allow_unauthenticated_remote"`
	// Retention deletes finished runs (DB rows + log dirs) older than this
	// duration, e.g. "2160h" for 90 days. "0" keeps everything forever.
	Retention      string `yaml:"retention"`
	AuditRetention string `yaml:"audit_retention"`
	// Global admission and execution bounds keep all trigger sources and pools
	// within predictable memory/process limits.
	MaxQueuedRunsGlobal int `yaml:"max_queued_runs_global"`
	MaxActiveRunsGlobal int `yaml:"max_active_runs_global"`
	MaxConcurrentTasks  int `yaml:"max_concurrent_tasks"`
	// KeyFile holds the hex key that encrypts connection passwords at rest.
	// Auto-generated (0600) on first serve. "none" disables encryption.
	KeyFile string `yaml:"key_file"`
	// WorkerListen enables the dial-in worker hub: a mTLS gRPC listener remote
	// workers connect to (e.g. ":9091"). Empty = no worker hub (single-binary
	// default). The embedded CA lives next to the key file.
	WorkerListen string `yaml:"worker_listen"`
	// WorkerAdvertise is the host:port workers are told to dial at join time
	// (defaults to the join request's host + the WorkerListen port). Set it
	// when the scheduler sits behind NAT or a load balancer.
	WorkerAdvertise string `yaml:"worker_advertise"`
	// WorkerJoinTokens pre-seeds one-time join tokens at startup (24h TTL,
	// hashed at rest) so an orchestrated stack (docker compose, k8s) can join
	// workers without a manual mint step. Comma-separated in the env form.
	WorkerJoinTokens []string `yaml:"worker_join_tokens"`
	// Notify is the instance-wide default alert destination: DAGs without their
	// own notify settings alert here (failure-only unless they set notify_on),
	// and scheduler-level events (executor down, retention failures) post here
	// too. Group names an alert group and wins over URL when both are set; URL
	// also accepts mailto:addr[,addr] once smtp is configured.
	Notify struct {
		URL    string `yaml:"url"`
		Format string `yaml:"format"` // ""/raw | slack | feishu | dingtalk | email
		Group  string `yaml:"group"`
	} `yaml:"notify"`
	// SMTP is the mail relay behind mailto: notify targets. Password accepts an
	// enc:v1: value encrypted with the key file (same scheme as connections).
	// Port 465 = implicit TLS; otherwise STARTTLS is required unless
	// allow_plaintext is set (lab relays).
	SMTP struct {
		Host           string `yaml:"host"`
		Port           int    `yaml:"port"` // default 587
		Username       string `yaml:"username"`
		Password       string `yaml:"password"`
		From           string `yaml:"from"` // default: username
		AllowPlaintext bool   `yaml:"allow_plaintext"`
	} `yaml:"smtp"`
	// Log controls the process-wide logger: level debug|info|warn|error,
	// format text|json (json for Loki/ELK ingestion).
	Log struct {
		Level  string `yaml:"level"`
		Format string `yaml:"format"`
	} `yaml:"log"`
	Auth struct {
		Enabled        bool     `yaml:"enabled"`
		SessionTTL     string   `yaml:"session_ttl"`
		SecureCookie   bool     `yaml:"secure_cookie"`
		AdminUser      string   `yaml:"admin_user,omitempty"`     // non-secret init default; legacy bootstrap username
		AdminPassword  string   `yaml:"admin_password,omitempty"` // legacy only; init never writes this
		TrustedProxies []string `yaml:"trusted_proxies,omitempty"`
	} `yaml:"auth"`
}

func defaultConfig() Config {
	c := Config{DB: "data/cronova.db", Dags: "dags", Logs: "logs", Tick: "2s", HTTP: "127.0.0.1:8090",
		Retention: "2160h", AuditRetention: "8760h", // runs 90 days; audit 365 days
		KeyFile: "cronova.key", MaxQueuedRunsGlobal: 10000, MaxActiveRunsGlobal: 1000, MaxConcurrentTasks: 64}
	c.Auth.SessionTTL = "24h"
	return c
}

// parseRetention parses the retention setting: a Go duration ("720h"), or "0"
// to disable pruning. Negative values are rejected.
func parseRetention(s string) (time.Duration, error) {
	if s == "" || s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid retention %q (use a duration like 2160h, or 0 to disable): %v", s, err)
	}
	return d, nil
}

// loadConfigFile overlays a YAML file onto c (only if the file exists). A given
// path that does not exist is an error; the default path being absent is fine.
func loadConfigFile(c *Config, path string, explicit bool) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return nil // default config file simply not present
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

// applyEnv overlays CRONOVA_* environment variables onto c.
func applyEnv(c *Config) {
	env := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	env("CRONOVA_DB", &c.DB)
	env("CRONOVA_DAGS", &c.Dags)
	env("CRONOVA_LOGS", &c.Logs)
	env("CRONOVA_PROJECTS", &c.Projects)
	env("CRONOVA_WORKSPACES", &c.Workspaces)
	env("CRONOVA_TICK", &c.Tick)
	env("CRONOVA_RELOAD", &c.Reload)
	env("CRONOVA_EXECUTOR", &c.Executor)
	env("CRONOVA_HTTP", &c.HTTP)
	if v, ok := os.LookupEnv("CRONOVA_ALLOW_UNAUTHENTICATED_REMOTE"); ok {
		if b, valid := parseBool(v); valid {
			c.AllowUnauthenticatedRemote = b
		}
	}
	env("CRONOVA_RETENTION", &c.Retention)
	env("CRONOVA_AUDIT_RETENTION", &c.AuditRetention)
	env("CRONOVA_KEY_FILE", &c.KeyFile)
	env("CRONOVA_WORKER_LISTEN", &c.WorkerListen)
	env("CRONOVA_WORKER_ADVERTISE", &c.WorkerAdvertise)
	if v, ok := os.LookupEnv("CRONOVA_WORKER_JOIN_TOKENS"); ok {
		c.WorkerJoinTokens = nil
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				c.WorkerJoinTokens = append(c.WorkerJoinTokens, t)
			}
		}
	}
	env("CRONOVA_NOTIFY_URL", &c.Notify.URL)
	env("CRONOVA_NOTIFY_FORMAT", &c.Notify.Format)
	env("CRONOVA_NOTIFY_GROUP", &c.Notify.Group)
	env("CRONOVA_SMTP_HOST", &c.SMTP.Host)
	if v, ok := os.LookupEnv("CRONOVA_SMTP_PORT"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			c.SMTP.Port = n
		}
	}
	env("CRONOVA_SMTP_USERNAME", &c.SMTP.Username)
	env("CRONOVA_SMTP_PASSWORD", &c.SMTP.Password)
	env("CRONOVA_SMTP_FROM", &c.SMTP.From)
	env("CRONOVA_LOG_LEVEL", &c.Log.Level)
	env("CRONOVA_LOG_FORMAT", &c.Log.Format)
	envInt := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				*dst = n
			}
		}
	}
	envInt("CRONOVA_MAX_QUEUED_RUNS_GLOBAL", &c.MaxQueuedRunsGlobal)
	envInt("CRONOVA_MAX_ACTIVE_RUNS_GLOBAL", &c.MaxActiveRunsGlobal)
	envInt("CRONOVA_MAX_CONCURRENT_TASKS", &c.MaxConcurrentTasks)
	if v, ok := os.LookupEnv("CRONOVA_AUTH"); ok {
		// Only a RECOGNIZED value flips the control; an unknown/blank value keeps
		// the current setting rather than failing open (auth defaults on for a
		// fresh install, so a typo like "True" or "on" must not silently disable it).
		if b, valid := parseBool(v); valid {
			c.Auth.Enabled = b
		}
	}
	env("CRONOVA_SESSION_TTL", &c.Auth.SessionTTL)
	if v, ok := os.LookupEnv("CRONOVA_SECURE_COOKIE"); ok {
		if b, valid := parseBool(v); valid {
			c.Auth.SecureCookie = b
		}
	}
	env("CRONOVA_ADMIN_USER", &c.Auth.AdminUser)
	env("CRONOVA_ADMIN_PASSWORD", &c.Auth.AdminPassword)
	if v, ok := os.LookupEnv("CRONOVA_TRUSTED_PROXIES"); ok {
		c.Auth.TrustedProxies = strings.Split(v, ",")
	}
}

// parseBool parses a boolean-ish env value leniently (case-insensitive, trimmed).
// valid is false for unrecognized or blank input, so callers can keep a secure
// default instead of failing open on an unexpected value.
func parseBool(s string) (val, valid bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "y", "enable", "enabled":
		return true, true
	case "0", "false", "no", "off", "n", "disable", "disabled":
		return false, true
	default:
		return false, false
	}
}

// sessionTTL parses the configured TTL, falling back to 24h on empty/invalid.
func (c Config) sessionTTL() time.Duration {
	if d, err := time.ParseDuration(c.Auth.SessionTTL); err == nil && d > 0 {
		return d
	}
	return 24 * time.Hour
}

// overlaySetFlags copies only the flags the user explicitly set on the command
// line onto c, so a flag always wins over env/file. Names must match the FlagSet.
func overlaySetFlags(c *Config, fs *flag.FlagSet, vals map[string]any) {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	str := func(name string, dst *string) {
		if set[name] {
			*dst = *vals[name].(*string)
		}
	}
	str("db", &c.DB)
	str("dags", &c.Dags)
	str("logs", &c.Logs)
	str("projects", &c.Projects)
	str("workspaces", &c.Workspaces)
	str("executor", &c.Executor)
	str("http", &c.HTTP)
	if set["tick"] {
		c.Tick = vals["tick"].(*time.Duration).String()
	}
	if set["reload"] {
		c.Reload = vals["reload"].(*time.Duration).String()
	}
	if set["retention"] {
		c.Retention = vals["retention"].(*time.Duration).String()
	}
	if set["audit-retention"] {
		c.AuditRetention = vals["audit-retention"].(*time.Duration).String()
	}
	integer := func(name string, dst *int) {
		if set[name] {
			*dst = *vals[name].(*int)
		}
	}
	integer("max-queued-runs", &c.MaxQueuedRunsGlobal)
	integer("max-active-runs", &c.MaxActiveRunsGlobal)
	integer("max-concurrent-tasks", &c.MaxConcurrentTasks)
	if set["auth"] {
		c.Auth.Enabled = *vals["auth"].(*bool)
	}
	if set["allow-unauthenticated-remote"] {
		c.AllowUnauthenticatedRemote = *vals["allow-unauthenticated-remote"].(*bool)
	}
}

// isLoopbackHTTPAddr reports whether addr is a TCP host:port reachable only
// through a loopback interface. An empty host such as ":8090" binds all
// interfaces and is therefore not loopback-only.
func isLoopbackHTTPAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateHTTPExposure(c Config) error {
	if c.HTTP == "" || c.Auth.Enabled || isLoopbackHTTPAddr(c.HTTP) || c.AllowUnauthenticatedRemote {
		return nil
	}
	return fmt.Errorf("refusing unauthenticated non-loopback HTTP bind %q: enable auth or explicitly set allow_unauthenticated_remote / -allow-unauthenticated-remote", c.HTTP)
}
