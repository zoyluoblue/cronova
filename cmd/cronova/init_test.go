package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

func TestRenderConfigYAMLRoundTripsAllOperationalFields(t *testing.T) {
	c := defaultConfig()
	c.DB = "/srv/cronova/meta.db"
	c.Dags = "/srv/cronova/dags"
	c.Logs = "/srv/cronova/logs"
	c.Projects = "/srv/cronova/projects"
	c.Workspaces = "/srv/cronova/workspaces"
	c.HTTP = "127.0.0.1:9000"
	c.Tick = "5s"
	c.Executor = "unix:///run/cronova/executor.sock"
	c.Retention = "720h"
	c.AuditRetention = "4320h"
	c.KeyFile = "/srv/cronova/cronova.key"
	c.AllowUnauthenticatedRemote = true
	c.MaxQueuedRunsGlobal = 4321
	c.MaxActiveRunsGlobal = 321
	c.MaxConcurrentTasks = 23
	c.Auth.Enabled = true
	c.Auth.SessionTTL = "12h"
	c.Auth.SecureCookie = true
	c.Auth.TrustedProxies = []string{"127.0.0.1", "10.0.0.0/8"}
	c.Auth.AdminUser = "remember-me"
	c.Auth.AdminPassword = "must-not-render"

	path := filepath.Join(t.TempDir(), "cronova.yaml")
	if err := os.WriteFile(path, []byte(renderConfigYAML(c)), 0o600); err != nil {
		t.Fatal(err)
	}
	got := defaultConfig()
	if err := loadConfigFile(&got, path, true); err != nil {
		t.Fatal(err)
	}
	if got.DB != c.DB || got.Dags != c.Dags || got.Logs != c.Logs || got.Projects != c.Projects ||
		got.Workspaces != c.Workspaces || got.HTTP != c.HTTP || got.Tick != c.Tick ||
		got.Executor != c.Executor || got.Retention != c.Retention || got.AuditRetention != c.AuditRetention ||
		got.KeyFile != c.KeyFile || got.AllowUnauthenticatedRemote != c.AllowUnauthenticatedRemote ||
		got.MaxQueuedRunsGlobal != c.MaxQueuedRunsGlobal || got.MaxActiveRunsGlobal != c.MaxActiveRunsGlobal ||
		got.MaxConcurrentTasks != c.MaxConcurrentTasks || got.Auth.Enabled != c.Auth.Enabled ||
		got.Auth.SessionTTL != c.Auth.SessionTTL || got.Auth.SecureCookie != c.Auth.SecureCookie ||
		got.Auth.AdminUser != c.Auth.AdminUser ||
		strings.Join(got.Auth.TrustedProxies, ",") != strings.Join(c.Auth.TrustedProxies, ",") {
		t.Fatalf("config did not round-trip: got=%+v want=%+v", got, c)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "must-not-render") {
		t.Fatal("bootstrap credentials leaked into config")
	}
}

func TestInitSeedsAndRotatesAdminWithoutStoringPassword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data", "cronova.db")
	configPath := filepath.Join(dir, "cronova.yaml")
	envPath := filepath.Join(dir, "cronova.env")
	t.Setenv("CRONOVA_DB", dbPath)
	t.Setenv("CRONOVA_HTTP", "127.0.0.1:8090")
	t.Setenv("CRONOVA_AUTH", "true")
	t.Setenv("CRONOVA_ADMIN_USER", "root-admin")
	t.Setenv("CRONOVA_ADMIN_PASSWORD", "first-password")
	if err := cmdInit([]string{"-yes", "-config", configPath, "-env", envPath}); err != nil {
		t.Fatal(err)
	}

	st, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.GetUserByUsername(context.Background(), "root-admin")
	if err != nil || !auth.CheckPassword(u.PasswordHash, "first-password") {
		t.Fatalf("admin was not seeded: user=%+v err=%v", u, err)
	}
	_ = st.Close()
	envRaw, _ := os.ReadFile(envPath)
	configRaw, _ := os.ReadFile(configPath)
	if strings.Contains(string(envRaw), "first-password") || strings.Contains(string(configRaw), "first-password") {
		t.Fatal("plaintext admin password was persisted")
	}

	legacyEnv := "CRONOVA_SECURE_COOKIE=true\nCRONOVA_ADMIN_USER=legacy\nCRONOVA_ADMIN_PASSWORD=legacy-secret\n"
	if err := os.WriteFile(envPath, []byte(legacyEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Unsetenv("CRONOVA_ADMIN_USER"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRONOVA_ADMIN_PASSWORD", "")
	if err := cmdInit([]string{"-yes", "-config", configPath, "-env", envPath}); err != nil {
		t.Fatal(err)
	}
	envRaw, _ = os.ReadFile(envPath)
	if !strings.Contains(string(envRaw), "CRONOVA_SECURE_COOKIE=true") || strings.Contains(string(envRaw), "legacy-secret") || strings.Contains(string(envRaw), "CRONOVA_ADMIN_USER=") {
		t.Fatalf("re-run did not preserve safe overrides and scrub legacy credentials:\n%s", envRaw)
	}
	fi, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("env mode = %v; want 0600", fi.Mode().Perm())
	}
	st, err = openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	u, err = st.GetUserByUsername(context.Background(), "root-admin")
	if err != nil || !auth.CheckPassword(u.PasswordHash, "first-password") {
		_ = st.Close()
		t.Fatalf("blank re-run did not keep current password: err=%v", err)
	}
	_ = st.Close()

	t.Setenv("CRONOVA_ADMIN_PASSWORD", "second-password")
	if err := cmdInit([]string{"-yes", "-config", configPath, "-env", envPath}); err != nil {
		t.Fatal(err)
	}
	st, err = openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u, err = st.GetUserByUsername(context.Background(), "root-admin")
	if err != nil || auth.CheckPassword(u.PasswordHash, "first-password") || !auth.CheckPassword(u.PasswordHash, "second-password") {
		t.Fatalf("admin password was not rotated: err=%v", err)
	}
}

func TestSeedAdminSamePasswordKeepsSessionsChangedPasswordRevokes(t *testing.T) {
	st, err := openStore(filepath.Join(t.TempDir(), "cronova.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := seedAdmin(ctx, st, "admin", "same-password"); err != nil {
		t.Fatal(err)
	}
	u, _ := st.GetUserByUsername(ctx, "admin")
	se := &model.Session{Token: "session-token", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := st.CreateSession(ctx, se); err != nil {
		t.Fatal(err)
	}
	if err := seedAdmin(ctx, st, "admin", "same-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSession(ctx, se.Token); err != nil {
		t.Fatalf("same password revoked session: %v", err)
	}
	if err := seedAdmin(ctx, st, "admin", "changed-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSession(ctx, se.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("changed password did not revoke session: %v", err)
	}
}

func TestWriteFileModeRepairsExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cronova.env")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileMode(path, "new", 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v, want 0600", fi.Mode().Perm(), err)
	}
}
