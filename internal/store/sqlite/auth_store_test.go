package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
)

func TestUserCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("fresh store has %d users, want 0", n)
	}
	u := &model.User{Username: "alice", PasswordHash: "hash1", Role: model.RoleAdmin}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("CreateUser did not set ID")
	}
	// unique username
	if err := s.CreateUser(ctx, &model.User{Username: "alice", PasswordHash: "x", Role: model.RoleViewer}); err == nil {
		t.Fatal("duplicate username accepted")
	}

	got, err := s.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got.PasswordHash != "hash1" || got.Role != model.RoleAdmin {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if _, err := s.GetUserByUsername(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}
	if n, _ := s.CountUsers(ctx); n != 1 {
		t.Fatalf("CountUsers = %d, want 1", n)
	}
}

func TestSessionLifecycleAndExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := &model.User{Username: "bob", PasswordHash: "h", Role: model.RoleViewer}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	live := &model.Session{Token: "tok-live", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.CreateSession(ctx, live); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	var storedToken string
	if err := s.db.QueryRowContext(ctx, `SELECT token FROM sessions WHERE user_id=?`, u.ID).Scan(&storedToken); err != nil {
		t.Fatalf("read stored session token: %v", err)
	}
	if storedToken == live.Token || storedToken != hashSessionToken(live.Token) {
		t.Fatalf("stored token = %q, want only its SHA-256 digest", storedToken)
	}
	got, err := s.GetSession(ctx, "tok-live")
	if err != nil {
		t.Fatalf("GetSession(live): %v", err)
	}
	if got.UserID != u.ID {
		t.Fatalf("session user = %d, want %d", got.UserID, u.ID)
	}
	if got.Token != live.Token {
		t.Fatalf("returned token = %q, want caller token", got.Token)
	}

	// an already-expired session must read as absent (and be pruned)
	dead := &model.Session{Token: "tok-dead", UserID: u.ID, ExpiresAt: time.Now().Add(-time.Minute)}
	if err := s.CreateSession(ctx, dead); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "tok-dead"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session err = %v, want ErrNotFound", err)
	}

	// logout
	if err := s.DeleteSession(ctx, "tok-live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(ctx, "tok-live"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session err = %v, want ErrNotFound", err)
	}
}

func TestMigrateHashesLegacySessionTokens(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := &model.User{Username: "legacy", PasswordHash: "h", Role: model.RoleViewer}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	const raw = "legacy-raw-cookie-token"
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token, user_id, expires_at) VALUES (?,?,?)`, raw, u.ID, fmtTime(time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT token FROM sessions WHERE user_id=?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hashSessionToken(raw) {
		t.Fatalf("migrated token = %q, want %q", stored, hashSessionToken(raw))
	}
	if _, err := s.GetSession(ctx, raw); err != nil {
		t.Fatalf("legacy browser token stopped working after migration: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("session migration is not idempotent: %v", err)
	}
}

func TestUpdatePasswordRevokesSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := &model.User{Username: "carol", PasswordHash: "old", Role: model.RoleAdmin}
	if err := s.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, &model.Session{Token: "sess", UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUserPassword(ctx, u.ID, "new"); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}
	// password changed
	got, _ := s.GetUserByID(ctx, u.ID)
	if got.PasswordHash != "new" {
		t.Fatalf("password hash = %q, want new", got.PasswordHash)
	}
	// sessions revoked
	if _, err := s.GetSession(ctx, "sess"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("session survived password change (should be revoked)")
	}
}
