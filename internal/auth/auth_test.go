package auth

import "testing"

func TestHashCheckRoundtrip(t *testing.T) {
	h, err := HashPassword("s3cret-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h == "s3cret-pass" {
		t.Fatal("hash equals plaintext")
	}
	if !CheckPassword(h, "s3cret-pass") {
		t.Fatal("correct password rejected")
	}
	if CheckPassword(h, "wrong-pass") {
		t.Fatal("wrong password accepted")
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password are identical (missing salt)")
	}
	if !CheckPassword(a, "same") || !CheckPassword(b, "same") {
		t.Fatal("salted hashes fail to verify")
	}
}

func TestCheckPasswordMalformed(t *testing.T) {
	for _, bad := range []string{"", "plain", "pbkdf2_sha256$x$y", "pbkdf2_sha256$0$$", "md5$1$a$b"} {
		if CheckPassword(bad, "anything") {
			t.Fatalf("malformed hash %q accepted", bad)
		}
	}
}

func TestNewSessionTokenUnique(t *testing.T) {
	a, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewSessionToken()
	if a == b {
		t.Fatal("session tokens collided")
	}
	if len(a) != 64 {
		t.Fatalf("token len = %d, want 64", len(a))
	}
}
