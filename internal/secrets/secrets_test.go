package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	copy(key, "0123456789abcdef0123456789abcdef")
	c, err := NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Encrypt("s3cret-Pa55!")
	if err != nil {
		t.Fatal(err)
	}
	if !IsEncrypted(sealed) || strings.Contains(sealed, "s3cret") {
		t.Fatalf("sealed value looks wrong: %q", sealed)
	}
	// no double encryption
	again, _ := c.Encrypt(sealed)
	if again != sealed {
		t.Fatal("re-encrypting a sealed value must be a no-op")
	}
	plain, err := c.Decrypt(sealed)
	if err != nil || plain != "s3cret-Pa55!" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	// legacy plaintext passes through
	if p, err := c.Decrypt("legacy-plain"); err != nil || p != "legacy-plain" {
		t.Fatalf("plaintext passthrough = %q, %v", p, err)
	}
	// empty stays empty
	if e, _ := c.Encrypt(""); e != "" {
		t.Fatal("empty password must stay empty")
	}
	// wrong key -> error, not garbage
	key2 := make([]byte, 32)
	copy(key2, "ffffffffffffffffffffffffffffffff")
	c2, _ := NewCipher(key2)
	if _, err := c2.Decrypt(sealed); err == nil {
		t.Fatal("wrong key must fail to decrypt")
	}
}

func TestLoadOrCreateKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cronova.key")
	k1, created, err := LoadOrCreateKeyFile(path)
	if err != nil || !created || len(k1) != 32 {
		t.Fatalf("first load: created=%v len=%d err=%v", created, len(k1), err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("key directory mode = %v, want 0700", fi.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	k2, created, err := LoadOrCreateKeyFile(path)
	if err != nil || created || string(k2) != string(k1) {
		t.Fatalf("second load must return the same key without creating")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("existing key file mode was not repaired: %v", fi.Mode().Perm())
	}
	// corrupted file is an error, not a silent new key
	os.WriteFile(path, []byte("nonsense"), 0o600)
	if _, _, err := LoadOrCreateKeyFile(path); err == nil {
		t.Fatal("corrupt key file must error")
	}
}
