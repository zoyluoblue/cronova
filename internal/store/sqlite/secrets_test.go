package sqlite

import (
	"context"
	"testing"

	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/secrets"
)

// With a cipher installed, passwords are sealed on disk but transparent through
// the store API; legacy plaintext rows keep working and are upgraded by the
// migration.
func TestConnectionPasswordEncryption(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// legacy plaintext row written BEFORE encryption was enabled
	if err := s.UpsertConnection(ctx, &model.Connection{ID: "legacy", Type: "mysql", Host: "h", Port: 3306, Login: "u", Password: "plain-old"}); err != nil {
		t.Fatal(err)
	}

	key := make([]byte, 32)
	copy(key, "0123456789abcdef0123456789abcdef")
	cip, _ := secrets.NewCipher(key)
	s.SetSecretCipher(cip)

	// new writes are sealed on disk…
	if err := s.UpsertConnection(ctx, &model.Connection{ID: "wh", Type: "postgres", Host: "h", Port: 5432, Login: "u", Password: "top-secret"}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT password FROM connections WHERE id='wh'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !secrets.IsEncrypted(raw) {
		t.Fatalf("password on disk is not sealed: %q", raw)
	}
	// …and transparent through the API
	c, err := s.GetConnection(ctx, "wh")
	if err != nil || c.Password != "top-secret" {
		t.Fatalf("GetConnection password = %q, %v", c.Password, err)
	}

	// legacy plaintext row still reads fine
	if c, err := s.GetConnection(ctx, "legacy"); err != nil || c.Password != "plain-old" {
		t.Fatalf("legacy password = %q, %v", c.Password, err)
	}

	// migration seals the legacy row in place, once
	n, err := s.MigrateConnectionSecrets(ctx)
	if err != nil || n != 1 {
		t.Fatalf("migrate = %d, %v; want 1", n, err)
	}
	if err := s.db.QueryRow(`SELECT password FROM connections WHERE id='legacy'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !secrets.IsEncrypted(raw) {
		t.Fatal("legacy row was not sealed by migration")
	}
	if c, err := s.GetConnection(ctx, "legacy"); err != nil || c.Password != "plain-old" {
		t.Fatalf("post-migration legacy password = %q, %v", c.Password, err)
	}
	if n, _ := s.MigrateConnectionSecrets(ctx); n != 0 {
		t.Fatalf("second migration should be a no-op, sealed %d", n)
	}
}
