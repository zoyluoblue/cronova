// Package auth holds password hashing and session-token helpers shared by the
// HTTP API (login) and the CLI (user management).
//
// Passwords are hashed with PBKDF2-HMAC-SHA256 (stdlib crypto/pbkdf2, Go 1.24+)
// using a per-password random salt and a high iteration count — an OWASP-
// acceptable KDF that keeps cronova pure-stdlib (no CGO, no external crypto dep).
// The encoded form is self-describing: "pbkdf2_sha256$<iter>$<salt>$<hash>".
// Session tokens are 256 bits of crypto/rand.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	pbkdf2Iter = 600000 // OWASP 2023 minimum for PBKDF2-HMAC-SHA256
	saltLen    = 16
	keyLen     = 32
)

// HashPassword returns an encoded PBKDF2 hash of the plaintext password.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", pbkdf2Iter, enc(salt), enc(dk)), nil
}

// CheckPassword reports whether password matches the encoded hash. The final
// comparison is constant-time; a malformed hash or mismatch both return false.
func CheckPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewSessionToken returns a random 256-bit token as a 64-char hex string.
func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// APITokenPrefix labels cronova bearer tokens so they are recognizable in logs
// and secret scanners (e.g. "cnv_pat_" = cronova personal access token).
const APITokenPrefix = "cnv_pat_"

// NewAPIToken mints a bearer token: the plaintext (shown once) and its SHA-256
// hex hash (stored). The plaintext is APITokenPrefix + 256 bits of base64url.
func NewAPIToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashAPIToken(plaintext), nil
}

// HashAPIToken returns the lowercase SHA-256 hex of a bearer token. Bearer
// tokens are high-entropy random values, so a plain (unsalted) hash is
// sufficient — it lets us look up by hash and never store the plaintext.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
