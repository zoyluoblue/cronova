package main

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config mirrors the `serve` settings. Precedence (highest first):
// explicit flag > CRONOVA_* env > config file > built-in default.
type Config struct {
	DB       string `yaml:"db"`
	Dags     string `yaml:"dags"`
	Logs     string `yaml:"logs"`
	Projects string `yaml:"projects"` // dir of uploaded project files ("" = ~/.cronova/projects)
	Tick     string `yaml:"tick"`
	Executor string `yaml:"executor"`
	HTTP     string `yaml:"http"`
	// AllowUnauthenticatedRemote is an explicit escape hatch for binding the
	// unauthenticated console to a non-loopback address.
	AllowUnauthenticatedRemote bool `yaml:"allow_unauthenticated_remote"`
	// Retention deletes finished runs (DB rows + log dirs) older than this
	// duration, e.g. "2160h" for 90 days. "0" keeps everything forever.
	Retention string `yaml:"retention"`
	// KeyFile holds the hex key that encrypts connection passwords at rest.
	// Auto-generated (0600) on first serve. "none" disables encryption.
	KeyFile string `yaml:"key_file"`
	Auth    struct {
		Enabled       bool   `yaml:"enabled"`
		SessionTTL    string `yaml:"session_ttl"`
		SecureCookie  bool   `yaml:"secure_cookie"`
		AdminUser     string `yaml:"admin_user"`     // seed admin on first run (empty = skip)
		AdminPassword string `yaml:"admin_password"` // prefer the env var over a file
	} `yaml:"auth"`
}

func defaultConfig() Config {
	c := Config{DB: "data/cronova.db", Dags: "dags", Logs: "logs", Tick: "2s", HTTP: "127.0.0.1:8090",
		Retention: "2160h", // 90 days; "0" = keep forever
		KeyFile:   "cronova.key"}
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
	env("CRONOVA_TICK", &c.Tick)
	env("CRONOVA_EXECUTOR", &c.Executor)
	env("CRONOVA_HTTP", &c.HTTP)
	if v, ok := os.LookupEnv("CRONOVA_ALLOW_UNAUTHENTICATED_REMOTE"); ok {
		if b, valid := parseBool(v); valid {
			c.AllowUnauthenticatedRemote = b
		}
	}
	env("CRONOVA_RETENTION", &c.Retention)
	env("CRONOVA_KEY_FILE", &c.KeyFile)
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
	str("executor", &c.Executor)
	str("http", &c.HTTP)
	if set["tick"] {
		c.Tick = vals["tick"].(*time.Duration).String()
	}
	if set["retention"] {
		c.Retention = vals["retention"].(*time.Duration).String()
	}
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
