package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// releaseRepo is where prebuilt release tarballs live (matches deploy/bootstrap.sh).
const releaseRepo = "zoyluoblue/cronova"

const (
	// maxBinary caps any single extracted executable.
	maxBinary = 256 << 20 // 256 MiB
	// Download bounds prevent a release endpoint from exhausting updater memory.
	maxReleaseArchiveBytes  = int64(512 << 20)
	maxReleaseMetadataBytes = int64(4 << 20)
)

// cmdUpdate replaces the installed cronova (and cronova-executor, if present)
// with a prebuilt release from GitHub, then restarts the managed service. It is
// the counterpart to the bootstrap installer: same asset naming, same checksum
// verification, but self-contained in the binary.
//
//	cronova update                              # latest release
//	cronova update v0.2.0                        # a specific tag (re-install / downgrade)
//	cronova update -proxy http://127.0.0.1:7890  # download through a proxy
//
// CRONOVA_BASE_URL overrides the download origin (private mirror / testing).
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	proxy := fs.String("proxy", "", "proxy for the download: http(s)://host:port or socks5://host:port, e.g. http://127.0.0.1:7890 (env CRONOVA_UPDATE_PROXY; also honors HTTPS_PROXY/ALL_PROXY)")
	pos := parsePositionals(fs, args)
	target := "latest"
	if len(pos) > 0 {
		target = pos[0]
	}

	proxyVal := *proxy
	if proxyVal == "" {
		proxyVal = os.Getenv("CRONOVA_UPDATE_PROXY")
	}
	if proxyVal != "" {
		if _, err := normalizeProxyURL(proxyVal); err != nil {
			return fmt.Errorf("invalid -proxy %q: %w", proxyVal, err)
		}
	}

	if err := ensureRoot("update"); err != nil {
		return err
	}

	base := releaseBaseURL(target)
	if err := validateBaseURL(base); err != nil {
		return err
	}
	asset := releaseAsset()
	if proxyVal != "" {
		fmt.Printf("cronova: fetching %s (%s) via proxy %s…\n", asset, target, proxyVal)
	} else {
		fmt.Printf("cronova: fetching %s (%s)…\n", asset, target)
	}
	bins, newVer, err := fetchRelease(base, asset, proxyVal)
	if err != nil {
		return err
	}

	// Short-circuit an unpinned update that is already current. (A pinned
	// version is always applied, so re-install and downgrade both work.)
	if target == "latest" && newVer != "" && newVer == version {
		fmt.Printf("cronova: already up to date (%s)\n", version)
		return nil
	}

	// Swap the binaries. cronova is required; the executor is only replaced when
	// the release ships one AND the host already has it (don't silently add it).
	var restores []func() error
	rollback := func() {
		for i := len(restores) - 1; i >= 0; i-- {
			_ = restores[i]()
		}
	}

	restore, err := swapBinary(binDst, bins["cronova"])
	if err != nil {
		return fmt.Errorf("replace %s: %w", binDst, err)
	}
	restores = append(restores, restore)

	if eb, ok := bins["cronova-executor"]; ok {
		if _, statErr := os.Stat(binExecutor); statErr == nil {
			r2, err := swapBinary(binExecutor, eb)
			if err != nil {
				rollback()
				return fmt.Errorf("replace %s: %w", binExecutor, err)
			}
			restores = append(restores, r2)
		}
	}

	// Refresh the service definition (plist/unit) from the release too, so a
	// format change in a new version actually takes effect — a binary-only swap
	// would pin the host to its bootstrap-era plist forever. Best-effort: a
	// failure here warns but does not abort the (already-swapped) binary update.
	if serviceInstalled() {
		if r3, err := refreshServiceDef(bins); err != nil {
			fmt.Fprintf(os.Stderr, "cronova: warning — could not refresh the service definition: %v\n", err)
		} else if r3 != nil {
			restores = append(restores, r3)
		}
	}

	// Restart the service so the new binary actually runs. On failure, roll the
	// binaries back and bring the previous version back up — never leave the box
	// on a half-applied update.
	if serviceInstalled() {
		fmt.Println("cronova: restarting service…")
		if err := restartService(); err != nil {
			fmt.Fprintln(os.Stderr, "cronova: restart failed — rolling back to the previous version")
			rollback()
			if restartErr := restartService(); restartErr != nil {
				return fmt.Errorf("update failed and the rollback restart also failed: %v (original: %w)", restartErr, err)
			}
			return fmt.Errorf("update aborted, restored previous version: %w", err)
		}
	}

	// Success — drop the .bak backups.
	commitSwap(binDst)
	commitSwap(binExecutor)
	if p := serviceDefPath(); p != "" {
		commitSwap(p)
	}

	from := version
	if from == "" || from == "dev" {
		from = "(unknown)"
	}
	to := newVer
	if to == "" {
		to = target
	}
	fmt.Printf("cronova: updated %s -> %s\n", from, to)
	if !serviceInstalled() {
		fmt.Println("cronova: binary replaced (no managed service to restart — start with `sudo cronova start` once installed).")
	}
	return nil
}

// releaseAsset is the tarball name for this host, e.g. cronova_darwin_arm64.tar.gz.
// GOOS/GOARCH already use the linux/darwin + amd64/arm64 spellings the packager emits.
func releaseAsset() string {
	return fmt.Sprintf("cronova_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

// releaseBaseURL is the directory holding <asset> and SHA256SUMS.
func releaseBaseURL(target string) string {
	if b := os.Getenv("CRONOVA_BASE_URL"); b != "" {
		return strings.TrimRight(b, "/")
	}
	if target == "latest" {
		return "https://github.com/" + releaseRepo + "/releases/latest/download"
	}
	return "https://github.com/" + releaseRepo + "/releases/download/" + target
}

// httpClient builds the download client. A non-empty proxy (http(s)/socks5)
// routes the download through it; empty falls back to the standard *_PROXY env
// vars (HTTPS_PROXY / HTTP_PROXY / ALL_PROXY) — a custom Transport does NOT read
// them unless we ask it to.
func httpClient(proxy string) *http.Client {
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if proxy != "" {
		if pu, err := normalizeProxyURL(proxy); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	} else {
		tr.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout:       10 * time.Minute,
		Transport:     tr,
		CheckRedirect: checkRedirect,
	}
}

// normalizeProxyURL turns a proxy spec into a URL, defaulting a bare host:port to
// an http proxy. Accepts http/https/socks5 (e.g. clash's 7890=http, 7891=socks5).
func normalizeProxyURL(s string) (*url.URL, error) {
	if !strings.Contains(s, "://") {
		s = "http://" + s // "127.0.0.1:7890" -> http proxy
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (use http, https, or socks5)", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy is missing host:port")
	}
	return u, nil
}

// checkRedirect refuses to follow a redirect that downgrades to cleartext — a
// self-updating root binary must never fetch its payload over http on the way to
// an https origin (matches deploy/bootstrap.sh's `curl --proto '=https'`). Plain
// http is allowed only to loopback (local mirror / testing).
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if isSecureURL(req.URL) {
		return nil
	}
	return fmt.Errorf("refusing insecure redirect to %s", req.URL)
}

// isSecureURL accepts https anywhere, and http only to a loopback host.
func isSecureURL(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && isLoopbackHost(u.Hostname())
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// validateBaseURL rejects an insecure CRONOVA_BASE_URL up front (the default
// GitHub origin is https, so only an override can trip this).
func validateBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("invalid update URL %q: %w", base, err)
	}
	if isSecureURL(u) {
		return nil
	}
	return fmt.Errorf("refusing insecure update origin %q — CRONOVA_BASE_URL must be https:// (http:// is allowed only for localhost)", base)
}

// fetchRelease requires SHA256SUMS, downloads the bounded tarball, verifies it,
// and only then extracts executable payloads. Missing or incomplete checksum
// metadata is fatal: a root self-updater must never install unverified bytes.
func fetchRelease(base, asset, proxy string) (bins map[string][]byte, version string, err error) {
	sums, err := httpGet(base+"/SHA256SUMS", proxy, maxReleaseMetadataBytes)
	if err != nil {
		return nil, "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, ok := sumFor(string(sums), asset)
	if !ok {
		return nil, "", fmt.Errorf("%s is not listed in SHA256SUMS", asset)
	}
	decoded, decodeErr := hex.DecodeString(want)
	if decodeErr != nil || len(decoded) != sha256.Size {
		return nil, "", fmt.Errorf("invalid SHA-256 digest for %s in SHA256SUMS", asset)
	}

	tarball, err := httpGet(base+"/"+asset, proxy, maxReleaseArchiveBytes)
	if err != nil {
		return nil, "", fmt.Errorf("download %s: %w — see https://github.com/%s/releases", asset, err, releaseRepo)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(tarball))
	if !strings.EqualFold(got, want) {
		return nil, "", fmt.Errorf("checksum mismatch for %s:\n  got  %s\n  want %s", asset, got, want)
	}
	fmt.Println("cronova: checksum OK")

	bins, version, err = extractReleaseBinaries(bytes.NewReader(tarball))
	if err != nil {
		return nil, "", err
	}
	if _, ok := bins["cronova"]; !ok {
		return nil, "", errors.New("release tarball does not contain a cronova binary")
	}
	return bins, version, nil
}

// httpGet returns the body of a 200 response; any other status or transport
// error is returned as an error.
func httpGet(url, proxy string, maxBytes int64) ([]byte, error) {
	resp, err := httpClient(proxy).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("response is too large: %d bytes (limit %d)", resp.ContentLength, maxBytes)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maxBytes)
	}
	return b, nil
}

// sumFor pulls the hex digest for a file out of a `sha256sum`-format listing
// ("<hex>  name" or "<hex> *name"), matching on the file's base name.
func sumFor(sums, asset string) (string, bool) {
	want := path.Base(asset)
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*") // binary-mode marker
		if path.Base(name) == want {
			return fields[0], true
		}
	}
	return "", false
}

// extractReleaseBinaries reads a cronova release tarball (gzip+tar) and returns
// the executable payloads keyed by base name plus the VERSION string.
func extractReleaseBinaries(r io.Reader) (map[string][]byte, string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, "", fmt.Errorf("gunzip release: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	out := make(map[string][]byte, 2)
	version := ""
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("read release tar: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		switch path.Base(h.Name) {
		case "cronova", "cronova-executor":
			if h.Size < 0 || h.Size > maxBinary {
				return nil, "", fmt.Errorf("release file %s is too large: %d bytes", path.Base(h.Name), h.Size)
			}
			b, err := io.ReadAll(io.LimitReader(tr, maxBinary))
			if err != nil {
				return nil, "", fmt.Errorf("extract %s: %w", path.Base(h.Name), err)
			}
			out[path.Base(h.Name)] = b
		case "com.cronova.plist", "cronova.service": // service definition, refreshed on update
			if h.Size < 0 || h.Size > 1<<20 {
				return nil, "", fmt.Errorf("release file %s is too large: %d bytes", path.Base(h.Name), h.Size)
			}
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, "", fmt.Errorf("extract %s: %w", path.Base(h.Name), err)
			}
			out[path.Base(h.Name)] = b
		case "VERSION":
			if h.Size < 0 || h.Size > 1<<10 {
				return nil, "", fmt.Errorf("release VERSION is too large: %d bytes", h.Size)
			}
			b, err := io.ReadAll(io.LimitReader(tr, 1<<10))
			if err == nil {
				version = strings.TrimSpace(string(b))
			}
		}
	}
	return out, version, nil
}

// swapBinary atomically replaces dst with data. It writes a sibling temp file
// (same filesystem, so the rename is atomic), backs the current binary up to
// dst.bak, then renames the new file into place. The returned restore func undoes
// the swap; on success the caller drops the backup with commitSwap.
//
// Replacing a *running* executable this way is safe on Unix: the rename swaps the
// directory entry while the old inode stays live for any process already exec'd
// from it — so cronova can update the very binary it is running from.
func swapBinary(dst string, data []byte) (restore func() error, err error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("refusing to install an empty %s", path.Base(dst))
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o755); err != nil { // WriteFile is subject to umask; force it
		os.Remove(tmp)
		return nil, err
	}

	bak := dst + ".bak"
	hadOld := false
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, bak); err != nil {
			os.Remove(tmp)
			return nil, err
		}
		hadOld = true
	}
	if err := os.Rename(tmp, dst); err != nil {
		if hadOld {
			_ = os.Rename(bak, dst) // best-effort restore
		}
		os.Remove(tmp)
		return nil, err
	}

	return func() error {
		if !hadOld {
			return os.Remove(dst)
		}
		return os.Rename(bak, dst) // overwrites the just-installed dst
	}, nil
}

// commitSwap drops the backup left by swapBinary/swapFile (best-effort).
func commitSwap(dst string) { _ = os.Remove(dst + ".bak") }

// serviceDefPath is the installed service-definition file for this host, or "".
func serviceDefPath() string {
	switch runtime.GOOS {
	case "darwin":
		return launchdPlist
	case "linux":
		return systemdUnitPath
	}
	return ""
}

// refreshServiceDef installs the service definition (launchd plist / systemd
// unit) shipped in the release over the installed one, so a format change takes
// effect on update rather than pinning the host to its bootstrap-era definition.
// Returns a restore func for the rollback chain, or nil when the release ships no
// definition / there's nothing installed to replace. The macOS plist is
// re-templated with the CURRENT service user so the daemon keeps its account.
func refreshServiceDef(bins map[string][]byte) (func() error, error) {
	switch runtime.GOOS {
	case "linux":
		def, ok := bins["cronova.service"]
		if !ok {
			return nil, nil
		}
		return swapFile(systemdUnitPath, def, 0o644, func() error { return run("systemctl", "daemon-reload") })
	case "darwin":
		def, ok := bins["com.cronova.plist"]
		if !ok {
			return nil, nil
		}
		user, group, err := readPlistUserGroup(launchdPlist)
		if err != nil {
			return nil, err
		}
		rendered := []byte(strings.NewReplacer("__USER__", user, "__GROUP__", group).Replace(string(def)))
		return swapFile(launchdPlist, rendered, 0o644, nil)
	}
	return nil, nil
}

// swapFile atomically replaces dst with data (backing dst up to dst.bak) and runs
// after() on success. The returned restore puts the backup back and re-runs
// after(). Mirrors swapBinary, for a config file.
func swapFile(dst string, data []byte, mode os.FileMode, after func() error) (func() error, error) {
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return nil, err
	}
	_ = os.Chmod(tmp, mode)
	bak := dst + ".bak"
	hadOld := false
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, bak); err != nil {
			os.Remove(tmp)
			return nil, err
		}
		hadOld = true
	}
	if err := os.Rename(tmp, dst); err != nil {
		if hadOld {
			_ = os.Rename(bak, dst)
		}
		os.Remove(tmp)
		return nil, err
	}
	// after() is the validation step (systemd daemon-reload). systemctl restart
	// uses the IN-MEMORY unit, so a syntactically bad NEW unit would restart fine
	// yet sit broken on disk until the next boot/reload — with the backup already
	// committed away. So if after() fails, the new definition is bad: undo the
	// swap, restore + reload the known-good one, and report, so the caller keeps it.
	if after != nil {
		if err := after(); err != nil {
			if hadOld {
				_ = os.Rename(bak, dst)
			} else {
				_ = os.Remove(dst)
			}
			_ = after() // reload the restored good definition
			return nil, fmt.Errorf("service definition rejected on reload: %w", err)
		}
	}
	return func() error {
		var err error
		if hadOld {
			err = os.Rename(bak, dst)
		} else {
			err = os.Remove(dst)
		}
		if after != nil {
			_ = after()
		}
		return err
	}, nil
}

var (
	plistUserRE  = regexp.MustCompile(`(?s)<key>UserName</key>\s*<string>([^<]*)</string>`)
	plistGroupRE = regexp.MustCompile(`(?s)<key>GroupName</key>\s*<string>([^<]*)</string>`)
)

// readPlistUserGroup extracts UserName/GroupName from the installed plist so the
// refreshed plist keeps the daemon running as the same (non-root) account.
func readPlistUserGroup(path string) (user, group string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	um := plistUserRE.FindSubmatch(b)
	gm := plistGroupRE.FindSubmatch(b)
	if um == nil || gm == nil {
		return "", "", fmt.Errorf("could not read UserName/GroupName from %s", path)
	}
	return string(um[1]), string(gm[1]), nil
}
