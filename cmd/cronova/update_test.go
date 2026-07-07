package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// makeTarGz builds an in-memory release tarball mirroring scripts/package.sh:
// entries are prefixed "./" as `tar -C stage .` emits them.
func makeTarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: "./" + name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(data)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReleaseAsset(t *testing.T) {
	want := fmt.Sprintf("cronova_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if got := releaseAsset(); got != want {
		t.Fatalf("releaseAsset() = %q, want %q", got, want)
	}
}

func TestReleaseBaseURL(t *testing.T) {
	t.Setenv("CRONOVA_BASE_URL", "") // ensure the override is off for the GitHub cases
	os.Unsetenv("CRONOVA_BASE_URL")
	cases := map[string]string{
		"latest": "https://github.com/" + releaseRepo + "/releases/latest/download",
		"v0.2.0": "https://github.com/" + releaseRepo + "/releases/download/v0.2.0",
	}
	for target, want := range cases {
		if got := releaseBaseURL(target); got != want {
			t.Errorf("releaseBaseURL(%q) = %q, want %q", target, got, want)
		}
	}
	t.Setenv("CRONOVA_BASE_URL", "http://localhost:9999/mirror/")
	if got, want := releaseBaseURL("latest"), "http://localhost:9999/mirror"; got != want {
		t.Errorf("override = %q, want %q (trailing slash trimmed)", got, want)
	}
}

func TestValidateBaseURL(t *testing.T) {
	ok := []string{
		"https://github.com/x/y/releases/latest/download",
		"http://localhost:8899",
		"http://127.0.0.1:9000/mirror",
		"http://[::1]:9000",
	}
	bad := []string{
		"http://mirror.internal/cronova",
		"http://example.com",
		"ftp://example.com",
	}
	for _, u := range ok {
		if err := validateBaseURL(u); err != nil {
			t.Errorf("validateBaseURL(%q) = %v, want nil", u, err)
		}
	}
	for _, u := range bad {
		if err := validateBaseURL(u); err == nil {
			t.Errorf("validateBaseURL(%q) = nil, want rejection", u)
		}
	}
}

func TestCheckRedirect(t *testing.T) {
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	if err := checkRedirect(req("https://objects.githubusercontent.com/x"), nil); err != nil {
		t.Errorf("https redirect should be allowed: %v", err)
	}
	if err := checkRedirect(req("http://localhost:8899/x"), nil); err != nil {
		t.Errorf("http-to-loopback redirect should be allowed: %v", err)
	}
	if err := checkRedirect(req("http://evil.example/x"), nil); err == nil {
		t.Error("https->http downgrade redirect to a remote host must be refused")
	}
	if err := checkRedirect(req("https://x/y"), make([]*http.Request, 10)); err == nil {
		t.Error("should stop after 10 redirects")
	}
}

func TestConfirmFrom(t *testing.T) {
	cases := map[string]bool{
		"y\n":   true,
		"yes\n": true,
		"Y\n":   true,
		"y":     true, // no trailing newline (Ctrl-D / printf 'y')
		"yes":   true,
		" y \n": true,
		"n\n":   false,
		"no\n":  false,
		"\n":    false,
		"":      false, // closed/empty stdin -> safe no
		"maybe": false,
	}
	for in, want := range cases {
		if got := confirmFrom(strings.NewReader(in)); got != want {
			t.Errorf("confirmFrom(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSumFor(t *testing.T) {
	sums := "abc123  cronova_linux_amd64.tar.gz\n" +
		"def456 *cronova_darwin_arm64.tar.gz\n" +
		"# a comment line\n"
	if got, ok := sumFor(sums, "cronova_linux_amd64.tar.gz"); !ok || got != "abc123" {
		t.Errorf("space form: got %q ok=%v", got, ok)
	}
	if got, ok := sumFor(sums, "cronova_darwin_arm64.tar.gz"); !ok || got != "def456" {
		t.Errorf("binary-mode(*) form: got %q ok=%v", got, ok)
	}
	if _, ok := sumFor(sums, "cronova_windows_amd64.tar.gz"); ok {
		t.Error("expected miss for an unlisted asset")
	}
}

func TestExtractReleaseBinaries(t *testing.T) {
	tb := makeTarGz(t, map[string][]byte{
		"cronova":              []byte("BINARY-A"),
		"cronova-executor":     []byte("BINARY-B"),
		"VERSION":              []byte("v9.9.9\n"),
		"cronova.yaml.example": []byte("ignored"),
	})
	bins, ver, err := extractReleaseBinaries(bytes.NewReader(tb))
	if err != nil {
		t.Fatal(err)
	}
	if string(bins["cronova"]) != "BINARY-A" || string(bins["cronova-executor"]) != "BINARY-B" {
		t.Fatalf("wrong binaries: %q / %q", bins["cronova"], bins["cronova-executor"])
	}
	if ver != "v9.9.9" {
		t.Fatalf("version = %q, want v9.9.9", ver)
	}
	if _, ok := bins["cronova.yaml.example"]; ok {
		t.Error("non-binary files should not be extracted")
	}
}

func TestFetchRelease(t *testing.T) {
	tb := makeTarGz(t, map[string][]byte{"cronova": []byte("hello"), "VERSION": []byte("v1.2.3")})
	sum := fmt.Sprintf("%x", sha256.Sum256(tb))
	asset := releaseAsset()

	newServer := func(sums string, serveSums bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch filepath.Base(r.URL.Path) {
			case asset:
				w.Write(tb)
			case "SHA256SUMS":
				if !serveSums {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, sums)
			default:
				http.NotFound(w, r)
			}
		}))
	}

	t.Run("verified", func(t *testing.T) {
		srv := newServer(sum+"  "+asset+"\n", true)
		defer srv.Close()
		bins, ver, err := fetchRelease(srv.URL, asset)
		if err != nil {
			t.Fatal(err)
		}
		if string(bins["cronova"]) != "hello" || ver != "v1.2.3" {
			t.Fatalf("got %q / %q", bins["cronova"], ver)
		}
	})

	t.Run("checksum mismatch is fatal", func(t *testing.T) {
		srv := newServer("deadbeef  "+asset+"\n", true)
		defer srv.Close()
		if _, _, err := fetchRelease(srv.URL, asset); err == nil {
			t.Fatal("expected a checksum-mismatch error")
		}
	})

	t.Run("missing SHA256SUMS still succeeds", func(t *testing.T) {
		srv := newServer("", false)
		defer srv.Close()
		if _, _, err := fetchRelease(srv.URL, asset); err != nil {
			t.Fatalf("missing sums should warn+skip, not fail: %v", err)
		}
	})

	t.Run("404 asset errors", func(t *testing.T) {
		srv := newServer("", true)
		defer srv.Close()
		if _, _, err := fetchRelease(srv.URL, "cronova_nope_nope.tar.gz"); err == nil {
			t.Fatal("expected a download error for a missing asset")
		}
	})
}

func TestSwapBinary(t *testing.T) {
	t.Run("replace existing with backup + restore", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "cronova")
		if err := os.WriteFile(dst, []byte("OLD"), 0o755); err != nil {
			t.Fatal(err)
		}
		restore, err := swapBinary(dst, []byte("NEW"))
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(dst); string(b) != "NEW" {
			t.Fatalf("after swap dst = %q, want NEW", b)
		}
		if b, _ := os.ReadFile(dst + ".bak"); string(b) != "OLD" {
			t.Fatalf("backup = %q, want OLD", b)
		}
		if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(dst); string(b) != "OLD" {
			t.Fatalf("after restore dst = %q, want OLD", b)
		}
	})

	t.Run("fresh install restore removes the new file", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "cronova")
		restore, err := swapBinary(dst, []byte("NEW"))
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(dst); string(b) != "NEW" {
			t.Fatalf("dst = %q, want NEW", b)
		}
		if err := restore(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Fatalf("restore should have removed dst, stat err = %v", err)
		}
	})

	t.Run("commitSwap drops the backup", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "cronova")
		os.WriteFile(dst, []byte("OLD"), 0o755)
		if _, err := swapBinary(dst, []byte("NEW")); err != nil {
			t.Fatal(err)
		}
		commitSwap(dst)
		if _, err := os.Stat(dst + ".bak"); !os.IsNotExist(err) {
			t.Fatalf("commitSwap should remove .bak, stat err = %v", err)
		}
	})

	t.Run("empty payload is refused", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "cronova")
		if _, err := swapBinary(dst, nil); err == nil {
			t.Fatal("expected refusal to install an empty binary")
		}
	})
}
