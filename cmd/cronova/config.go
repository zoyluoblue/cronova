package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config mirrors the `serve` settings. Precedence (highest first):
// explicit flag > CRONOVA_* env > config file > built-in default.
type Config struct {
	DB       string `yaml:"db"`
	Dags     string `yaml:"dags"`
	Logs     string `yaml:"logs"`
	Tick     string `yaml:"tick"`
	Executor string `yaml:"executor"`
	HTTP     string `yaml:"http"`
	Auth     struct {
		Enabled       bool   `yaml:"enabled"`
		SessionTTL    string `yaml:"session_ttl"`
		SecureCookie  bool   `yaml:"secure_cookie"`
		AdminUser     string `yaml:"admin_user"`     // seed admin on first run (empty = skip)
		AdminPassword string `yaml:"admin_password"` // prefer the env var over a file
	} `yaml:"auth"`
}

func defaultConfig() Config {
	c := Config{DB: "data/cronova.db", Dags: "dags", Logs: "logs", Tick: "2s", HTTP: ":8090"}
	c.Auth.SessionTTL = "24h"
	return c
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
	if err := yaml.Unmarshal(b, c); err != nil {
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
	env("CRONOVA_TICK", &c.Tick)
	env("CRONOVA_EXECUTOR", &c.Executor)
	env("CRONOVA_HTTP", &c.HTTP)
	if v, ok := os.LookupEnv("CRONOVA_AUTH"); ok {
		c.Auth.Enabled = v == "1" || v == "true" || v == "yes"
	}
	env("CRONOVA_SESSION_TTL", &c.Auth.SessionTTL)
	if v, ok := os.LookupEnv("CRONOVA_SECURE_COOKIE"); ok {
		c.Auth.SecureCookie = v == "1" || v == "true" || v == "yes"
	}
	env("CRONOVA_ADMIN_USER", &c.Auth.AdminUser)
	env("CRONOVA_ADMIN_PASSWORD", &c.Auth.AdminPassword)
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
	str("executor", &c.Executor)
	str("http", &c.HTTP)
	if set["tick"] {
		c.Tick = vals["tick"].(*time.Duration).String()
	}
	if set["auth"] {
		c.Auth.Enabled = *vals["auth"].(*bool)
	}
}
