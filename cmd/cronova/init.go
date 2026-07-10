package main

import (
	"bufio"
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdInit is the first-time setup wizard. Interactively (when stdin is a
// terminal) it walks through port, bind scope, admin account and auth, each with
// a default that Enter accepts; otherwise it takes defaults + CRONOVA_* env. It
// writes the server config (cronova.yaml) and a 0600 secrets file (cronova.env)
// holding the seed admin — `serve` creates that admin idempotently on first start.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", envOr("CRONOVA_CONFIG", "cronova.yaml"), "config file to write")
	envPath := fs.String("env", envOr("CRONOVA_ENV_FILE", "cronova.env"), "secrets file to write (admin seed)")
	yes := fs.Bool("yes", false, "non-interactive: accept defaults / env without prompting")
	_ = fs.Parse(args)

	// Prefill from an existing config so a re-run shows current values as defaults.
	cfg := defaultConfig()
	_, statErr := os.Stat(*configPath)
	fileExisted := statErr == nil // re-run (reconfigure) vs. fresh install
	if err := loadConfigFile(&cfg, *configPath, false); err != nil {
		return err
	}
	// CRONOVA_* overlays the file/defaults for the fields written to cronova.yaml
	// (http, tick, executor, auth.session_ttl, auth.secure_cookie), so a
	// non-interactive install can preset them; interactive prompts still override
	// the ones they cover. Storage paths (db/dags/logs) are intentionally left to
	// the service unit/plist flags, so they are not applied here.
	applyEnv(&cfg)
	port, bindAll := splitHTTP(cfg.HTTP)
	adminUser := envOr("CRONOVA_ADMIN_USER", "admin")
	adminPass := os.Getenv("CRONOVA_ADMIN_PASSWORD")

	// Resolve auth: a FRESH install defaults to on (secure); a re-run keeps the
	// existing file's choice; a RECOGNIZED CRONOVA_AUTH overrides either. An
	// unrecognized/blank CRONOVA_AUTH never disables auth (no fail-open).
	authEnabled := true
	if fileExisted {
		authEnabled = cfg.Auth.Enabled
	}
	if v, ok := os.LookupEnv("CRONOVA_AUTH"); ok {
		if b, valid := parseBool(v); valid {
			authEnabled = b
		}
	}
	genPW := false

	interactive := !*yes && isTerminal(os.Stdin)
	if interactive {
		in := bufio.NewReader(os.Stdin)
		fmt.Println("cronova setup — press Enter to accept the [default].")
		fmt.Println()

		port = promptLine(in, "HTTP port", port)
		bindAll = promptChoice(in, "Console reachable from",
			[]string{"all interfaces (0.0.0.0) — reachable by server IP",
				"this machine only (127.0.0.1) — use a reverse proxy / SSH tunnel"},
			boolToIdx(!bindAll)) == 0
		adminUser = promptLine(in, "Admin username", adminUser)

		for {
			p1 := promptSecret(in, "Admin password (blank = generate a strong one)")
			if p1 == "" {
				adminPass, genPW = randPassword(24), true
				break
			}
			if p2 := promptSecret(in, "Confirm password"); p1 == p2 {
				adminPass = p1
				break
			}
			fmt.Println("  passwords did not match, try again")
		}
		authEnabled = promptYesNo(in, "Require login for the console/API (recommended)", authEnabled)

		// the wizard owns the listen address; rebuild it from the prompts. In
		// non-interactive mode cfg.HTTP keeps its env/file/default value.
		bind := ""
		if !bindAll {
			bind = "127.0.0.1"
		}
		cfg.HTTP = bind + ":" + port
	} else if adminPass == "" {
		adminPass, genPW = randPassword(24), true
	}

	cfg.Auth.Enabled = authEnabled
	if err := validateHTTPExposure(cfg); err != nil {
		return fmt.Errorf("unsafe setup: %w", err)
	}

	if err := writeFileMode(*configPath, renderConfigYAML(cfg), 0o644); err != nil {
		return err
	}
	envBody := fmt.Sprintf("# cronova secrets — loaded by systemd (EnvironmentFile). Keep 0600.\n"+
		"CRONOVA_ADMIN_USER=%s\nCRONOVA_ADMIN_PASSWORD=%s\n"+
		"# CRONOVA_SECURE_COOKIE=true   # set when serving behind HTTPS\n", adminUser, adminPass)
	if err := writeFileMode(*envPath, envBody, 0o600); err != nil {
		return err
	}

	// summary
	fmt.Println()
	fmt.Printf("wrote %s and %s\n", *configPath, *envPath)
	host := "0.0.0.0"
	if !bindAll {
		host = "127.0.0.1"
	}
	fmt.Printf("  console : http://%s:%s   (auth %s)\n", host, port, onOff(authEnabled))
	fmt.Printf("  admin   : %s\n", adminUser)
	if genPW {
		fmt.Printf("  password: %s   (generated — save it)\n", adminPass)
	}
	return nil
}

// splitHTTP turns a listen address (":8090", "127.0.0.1:8090", "0.0.0.0:8090")
// into (port, bindAll). An empty/unspecified host means all interfaces.
func splitHTTP(addr string) (port string, bindAll bool) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil || p == "" {
		return "8090", true
	}
	return p, host == "" || host == "0.0.0.0" || host == "::"
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func promptLine(in *bufio.Reader, label, def string) string {
	fmt.Printf("%s [%s]: ", label, def)
	line, _ := in.ReadString('\n')
	if s := strings.TrimSpace(line); s != "" {
		return s
	}
	return def
}

func promptYesNo(in *bufio.Reader, label string, def bool) bool {
	d := "Y/n"
	if !def {
		d = "y/N"
	}
	fmt.Printf("%s [%s]: ", label, d)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

// promptChoice prints numbered options and returns the selected 0-based index.
func promptChoice(in *bufio.Reader, label string, options []string, def int) int {
	fmt.Println(label + ":")
	for i, o := range options {
		fmt.Printf("  %d) %s\n", i+1, o)
	}
	fmt.Printf("choose [%d]: ", def+1)
	line, _ := in.ReadString('\n')
	s := strings.TrimSpace(line)
	if s == "" {
		return def
	}
	for i := range options {
		if s == fmt.Sprintf("%d", i+1) {
			return i
		}
	}
	return def
}

// promptSecret reads a line with terminal echo disabled (via stty; the target is
// Linux). If stty is unavailable the input is simply echoed.
func promptSecret(in *bufio.Reader, label string) string {
	fmt.Printf("%s: ", label)
	setEcho(false)
	line, _ := in.ReadString('\n')
	setEcho(true)
	fmt.Println()
	return strings.TrimRight(line, "\r\n")
}

func setEcho(on bool) {
	arg := "-echo"
	if on {
		arg = "echo"
	}
	c := exec.Command("stty", arg)
	c.Stdin = os.Stdin
	_ = c.Run()
}

func boolToIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// randPassword returns n alphanumeric characters from crypto/rand.
func randPassword(n int) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is fatal for a security-relevant secret
		panic("cronova init: cannot read random source: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func writeFileMode(path, body string, mode os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(body), mode)
}

// renderConfigYAML produces a fully-commented cronova.yaml with the wizard's
// choices baked in. Paths (db/dags/logs) are intentionally left as comments:
// under systemd they are set by the unit's flags, which win over the file.
func renderConfigYAML(c Config) string {
	return fmt.Sprintf(`# cronova configuration — written by 'cronova init'.
# Precedence, highest first:  flag  >  CRONOVA_* env  >  this file  >  built-in default.

# HTTP address for the console + API. ":8090" = all interfaces;
# "127.0.0.1:8090" = this machine only (front with a reverse proxy for remote access).
http: "%s"

# DANGEROUS escape hatch. Required only when auth is disabled and http binds a
# non-loopback address. Keep false for normal deployments.
allow_unauthenticated_remote: %t

# Scheduler tick — how often the loop wakes to schedule/poll work.
tick: %s

# Task executor. Empty = in-process (tasks die on restart). A gRPC target such as
# "unix:///run/cronova/executor.sock" survives scheduler restarts (crash recovery).
# The socket's parent directory must be private (mode 0700).
executor: "%s"

auth:
  enabled: %t              # require login for the console + API
  session_ttl: %s          # how long a login session stays valid
  secure_cookie: %t        # set true when served over HTTPS (marks the cookie Secure)
  # Seed admin credentials live in the secrets file (cronova.env / CRONOVA_ADMIN_*),
  # not here, so this file can stay world-readable.

# Storage paths. Under systemd these are set by the service unit and this section
# is ignored; uncomment only for a manual (non-systemd) run.
# db:   /var/lib/cronova/cronova.db
# dags: /var/lib/cronova/dags
# logs: /var/log/cronova
`, c.HTTP, c.AllowUnauthenticatedRemote, c.Tick, c.Executor, c.Auth.Enabled, c.Auth.SessionTTL, c.Auth.SecureCookie)
}
