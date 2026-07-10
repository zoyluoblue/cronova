package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// cmdInit is the first-time setup wizard. Interactively (when stdin is a
// terminal) it walks through port, bind scope, admin account and auth, each with
// a default that Enter accepts; otherwise it takes defaults + CRONOVA_* env. It
// writes the server config (cronova.yaml), seeds or rotates the admin directly
// in SQLite, and writes a 0600 env-override template without credentials.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", envOr("CRONOVA_CONFIG", "cronova.yaml"), "config file to write")
	envPath := fs.String("env", envOr("CRONOVA_ENV_FILE", "cronova.env"), "0600 environment-override template to write")
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
	// the ones they cover. Every field is rendered back so reconfiguration is
	// lossless.
	applyEnv(&cfg)
	port, bindAll := splitHTTP(cfg.HTTP)
	adminDefault := strings.TrimSpace(cfg.Auth.AdminUser)
	if adminDefault == "" {
		adminDefault = "admin"
	}
	adminUser := envOr("CRONOVA_ADMIN_USER", adminDefault)
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
			passwordPrompt := "Admin password (blank = generate a strong one)"
			if fileExisted {
				passwordPrompt = "Admin password (blank = keep current)"
			}
			p1 := promptSecret(in, passwordPrompt)
			if p1 == "" {
				if !fileExisted {
					adminPass, genPW = randPassword(24), true
				} else {
					adminPass = ""
				}
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
	} else if adminPass == "" && !fileExisted {
		adminPass, genPW = randPassword(24), true
	}

	cfg.Auth.Enabled = authEnabled
	if err := validateHTTPExposure(cfg); err != nil {
		return fmt.Errorf("unsafe setup: %w", err)
	}
	// Seed or rotate the admin directly. The daemon never needs the plaintext in
	// its environment, so arbitrary trusted-host tasks cannot read a bootstrap
	// credential file. On a re-run with no password, leave the account untouched.
	if adminPass != "" {
		st, err := openStore(cfg.DB)
		if err != nil {
			return fmt.Errorf("initialize admin database: %w", err)
		}
		if err := seedAdmin(context.Background(), st, adminUser, adminPass); err != nil {
			_ = st.Close()
			return fmt.Errorf("initialize admin: %w", err)
		}
		if err := st.Close(); err != nil {
			return err
		}
	}
	cfg.Auth.AdminUser = adminUser // non-secret default for the next init run
	cfg.Auth.AdminPassword = ""

	if err := writeFileMode(*configPath, renderConfigYAML(cfg), 0o644); err != nil {
		return err
	}
	existingEnv, err := os.ReadFile(*envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", *envPath, err)
	}
	envBody := renderEnvOverrides(string(existingEnv))
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

func renderEnvOverrides(existing string) string {
	if strings.TrimSpace(existing) == "" {
		return "# Optional cronova environment overrides. Keep this file 0600.\n" +
			"# The admin password is seeded directly into the database by `cronova init`\n" +
			"# and is intentionally never stored here.\n" +
			"# CRONOVA_SECURE_COOKIE=true   # set when serving behind HTTPS\n"
	}
	lines := strings.Split(existing, "\n")
	kept := lines[:0]
	for _, line := range lines {
		candidate := strings.TrimSpace(line)
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "export "))
		if i := strings.IndexByte(candidate, '='); i >= 0 {
			switch strings.TrimSpace(candidate[:i]) {
			case "CRONOVA_ADMIN_USER", "CRONOVA_ADMIN_PASSWORD":
				continue // scrub legacy long-lived bootstrap credentials
			}
		}
		kept = append(kept, line)
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if strings.TrimSpace(body) == "" {
		return renderEnvOverrides("")
	}
	return body + "\n"
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
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// renderConfigYAML serializes every supported field. Re-running init is a
// lossless reconfiguration: storage, retention, key, executor, and resource
// bounds are not silently reset. The non-secret admin username is retained as
// the next wizard default; the password is always omitted.
func renderConfigYAML(c Config) string {
	c.Auth.AdminPassword = ""
	b, err := yaml.Marshal(c)
	if err != nil {
		panic("render config: " + err.Error())
	}
	return "# cronova configuration - written by 'cronova init'.\n" +
		"# Precedence: flag > CRONOVA_* env > this file > built-in default.\n\n" + string(b)
}
