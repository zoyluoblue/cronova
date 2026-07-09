// Package secrets encrypts connection passwords at rest. Values are sealed
// with AES-256-GCM and stored as "enc:v1:<base64(nonce||ciphertext)>", so a
// plaintext row (written before encryption was enabled) is recognized by the
// missing prefix and keeps working; it is upgraded in place by the one-time
// migration on startup and on its next save.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prefix = "enc:v1:"

// Cipher seals and opens connection secrets with a fixed 32-byte key.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a Cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// IsEncrypted reports whether a stored value carries the sealed-value prefix.
func IsEncrypted(s string) bool { return strings.HasPrefix(s, prefix) }

// Encrypt seals a plaintext. Empty stays empty (no password set); an already
// sealed value is returned unchanged so double-encryption cannot happen.
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" || IsEncrypted(plain) {
		return plain, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a stored value. A value without the prefix is legacy plaintext
// and is returned as-is; a sealed value that cannot be opened (wrong key,
// corruption) is an error rather than a silent wrong password.
func (c *Cipher) Decrypt(stored string) (string, error) {
	if !IsEncrypted(stored) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, prefix))
	if err != nil {
		return "", fmt.Errorf("decode sealed secret: %w", err)
	}
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", fmt.Errorf("sealed secret too short")
	}
	plain, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("open sealed secret (wrong key file?): %w", err)
	}
	return string(plain), nil
}

// LoadOrCreateKeyFile returns the 32-byte key stored hex-encoded at path,
// generating it (0600, parent dirs created) on first use. created reports
// whether a new key was minted — callers log that so operators know to back
// the file up.
func LoadOrCreateKeyFile(path string) (key []byte, created bool, err error) {
	b, err := os.ReadFile(path)
	if err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(b)))
		if derr != nil || len(key) != 32 {
			return nil, false, fmt.Errorf("key file %s is not a 64-char hex key", path)
		}
		return key, false, nil
	}
	if !os.IsNotExist(err) {
		return nil, false, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, false, err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, false, err
		}
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	return key, true, nil
}
