package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Service-control constants must match what the installers lay down:
// deploy/cronova.service (systemd) and deploy/com.cronova.plist (launchd).
const (
	systemdUnit     = "cronova"                             // `systemctl <verb> cronova`
	systemdUnitPath = "/etc/systemd/system/cronova.service" // installed unit file
	launchdLabel    = "com.cronova"                         // `launchctl ... system/com.cronova`
	launchdPlist    = "/Library/LaunchDaemons/com.cronova.plist"
)

// Install paths shared by update/uninstall. The binary destination is the SAME
// on both platforms (only the service definition and data dirs differ), which is
// what lets `update` swap the binary with one code path.
const (
	binDst      = "/usr/local/bin/cronova"
	binExecutor = "/usr/local/bin/cronova-executor"
)

// cmdService implements `cronova start|stop|restart|status` by driving the host
// service manager (systemd on Linux, launchd on macOS), so operators don't have
// to remember the platform-specific systemctl/launchctl incantations.
func cmdService(action string) error {
	switch runtime.GOOS {
	case "linux":
		return serviceSystemd(action)
	case "darwin":
		return serviceLaunchd(action)
	default:
		return fmt.Errorf("`cronova %s` needs a service manager (Linux/systemd or macOS/launchd); on %s run `cronova serve` directly", action, runtime.GOOS)
	}
}

// run execs a command inheriting stdio, so the service manager's own output and
// exit status flow straight through to the user.
func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// proxyEnvNames are the standard proxy env vars forwarded through sudo (upper-cased).
var proxyEnvNames = map[string]bool{"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true}

// ensureRoot guarantees the current process is root for a mutating action. If it
// is not, it transparently re-executes the exact same command under `sudo`
// (replacing this process, so sudo's exit status becomes ours) — the "one-click"
// experience: `cronova start` just works and prompts for a password when needed.
//
// CRONOVA_* env vars are forwarded through sudo explicitly (sudo's env_reset would
// otherwise strip e.g. CRONOVA_BASE_URL used by `update`). Set CRONOVA_NO_SUDO=1
// to opt out of auto-escalation (scripts that manage sudo themselves).
func ensureRoot(action string) error {
	if os.Geteuid() == 0 {
		return nil
	}
	if os.Getenv("CRONOVA_NO_SUDO") == "1" {
		return fmt.Errorf("`cronova %s` needs root — re-run: sudo cronova %s", action, action)
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return fmt.Errorf("`cronova %s` needs root, but sudo was not found — re-run as root", action)
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate own binary for privilege escalation: %w", err)
	}
	fmt.Fprintf(os.Stderr, "cronova: %q needs root — re-running under sudo…\n", action)

	// sudo VAR=val … cmd: forward our CRONOVA_* config AND the standard *_PROXY
	// vars across the env_reset barrier, so e.g. `HTTPS_PROXY=… cronova update`
	// still reaches the download after escalation.
	argv := []string{sudo}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CRONOVA_") || proxyEnvNames[strings.ToUpper(name)] {
			argv = append(argv, kv)
		}
	}
	argv = append(argv, self)
	argv = append(argv, os.Args[1:]...)

	// Replace this process; on success syscall.Exec never returns.
	if err := syscall.Exec(sudo, argv, os.Environ()); err != nil {
		return fmt.Errorf("sudo escalation failed: %w", err)
	}
	return nil // unreachable
}

func serviceSystemd(action string) error {
	switch action {
	case "start", "stop", "restart":
		if err := ensureRoot(action); err != nil {
			return err
		}
		return run("systemctl", action, systemdUnit)
	case "status":
		// read-only: systemctl status works unprivileged.
		return run("systemctl", "--no-pager", "status", systemdUnit)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func serviceLaunchd(action string) error {
	// status is read-only — never auto-escalate for it.
	if action == "status" {
		if !launchdLoaded() {
			if os.Geteuid() != 0 {
				fmt.Println("cronova: not loaded, or run `sudo cronova status` to query the system domain.")
			} else {
				fmt.Println("cronova: not loaded (launchd). Start it with: sudo cronova start")
			}
			return nil
		}
		return run("launchctl", "print", "system/"+launchdLabel)
	}

	if err := ensureRoot(action); err != nil { // launchctl's system domain needs root
		return err
	}
	loaded := launchdLoaded()

	installed := func() error {
		if _, err := os.Stat(launchdPlist); err != nil {
			return fmt.Errorf("service not installed (%s missing) — run the installer, or use `cronova serve` for a local run", launchdPlist)
		}
		return nil
	}

	switch action {
	case "start":
		if loaded { // already registered — (re)start it if it's not running
			return run("launchctl", "kickstart", "system/"+launchdLabel)
		}
		if err := installed(); err != nil {
			return err
		}
		return launchdBootstrap()
	case "stop":
		if !loaded {
			fmt.Println("cronova: already stopped.")
			return nil
		}
		// bootout (unload) is the reliable stop: KeepAlive would respawn a plain kill.
		return run("launchctl", "bootout", "system/"+launchdLabel)
	case "restart":
		if err := installed(); err != nil {
			return err
		}
		// Full unload+load — the reliable reload regardless of current run state
		// (running, idle, or crash-looping). `kickstart -k` needs a live instance
		// to kill and errors on a loaded-but-stopped job; this does not. Mirrors
		// deploy/install-macos.sh's start_service.
		return launchdReload(loaded)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

// launchdLoaded reports whether a job with our label is registered in launchd's
// system domain. Without root this can under-report (the query itself may be
// denied), which callers account for.
func launchdLoaded() bool {
	return exec.Command("launchctl", "print", "system/"+launchdLabel).Run() == nil
}

// launchdReload unloads (if loaded) and re-bootstraps the daemon. bootout is
// asynchronous, so it waits for the label to disappear before bootstrapping,
// avoiding the race where the new load collides with the still-tearing-down job.
func launchdReload(loaded bool) error {
	if loaded {
		_ = run("launchctl", "bootout", "system/"+launchdLabel) // may already be gone
		for i := 0; i < 40; i++ {                               // ~10s for the async teardown
			if !launchdLoaded() {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	return launchdBootstrap()
}

// launchdBootstrap enables + bootstraps the daemon and then CONFIRMS it actually
// stays running. This matters: `launchctl bootstrap` returns as soon as the job
// is LOADED, not when the program stays up — a bad binary (bad config, port
// conflict, panic) loads, then KeepAlive throttles the crash-loop to
// state=waiting *after* bootstrap already returned 0. Without the confirmation,
// `update` would treat a broken binary as a successful restart and discard its
// rollback backup. Mirrors deploy/install-macos.sh's start_service.
func launchdBootstrap() error {
	// enable clears any persistent `disabled` override (e.g. a prior `launchctl
	// disable`), matching the installer; bootstrap alone cannot load a disabled job.
	_ = run("launchctl", "enable", "system/"+launchdLabel)
	if err := run("launchctl", "bootstrap", "system", launchdPlist); err != nil {
		return err
	}
	return launchdConfirmRunning()
}

var launchdPidRE = regexp.MustCompile(`pid = \d+`)

// launchdConfirmRunning polls until the daemon reports a live, running pid,
// re-confirming after a beat so a start-then-crash (which flips to state=waiting)
// is caught rather than mistaken for success.
func launchdConfirmRunning() error {
	running := func() bool {
		out, _ := exec.Command("launchctl", "print", "system/"+launchdLabel).Output()
		s := string(out)
		return strings.Contains(s, "state = running") && launchdPidRE.MatchString(s)
	}
	for i := 0; i < 16; i++ { // ~8s to come up
		if running() {
			time.Sleep(1 * time.Second) // ensure it did not immediately crash
			if running() {
				return nil
			}
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("the launchd service did not stay running after load (inspect: sudo launchctl print system/%s)", launchdLabel)
}

// serviceInstalled reports whether cronova is installed as a native service on
// this host — used by `update` (restart only if managed) and `uninstall`.
func serviceInstalled() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := os.Stat(launchdPlist)
		return err == nil
	case "linux":
		_, err := os.Stat(systemdUnitPath)
		return err == nil
	}
	return false
}

// restartService restarts the installed service so a freshly-swapped binary takes
// effect, returning an error only once the service is confirmed running (so
// update's rollback fires on a broken binary). Callers must hold root.
func restartService() error {
	switch runtime.GOOS {
	case "darwin":
		return launchdReload(launchdLoaded())
	case "linux":
		if err := run("systemctl", "restart", systemdUnit); err != nil {
			return err
		}
		return systemdConfirmActive()
	default:
		return fmt.Errorf("cannot restart service on %s", runtime.GOOS)
	}
}

// systemdConfirmActive verifies the unit is active (not activating/auto-restart
// after a crash) and stays that way, so `restart` reports honest success. `Type=simple`
// units are considered started at fork, so `systemctl restart` alone is not proof.
func systemdConfirmActive() error {
	active := func() bool {
		return exec.Command("systemctl", "is-active", "--quiet", systemdUnit).Run() == nil
	}
	for i := 0; i < 10; i++ { // ~5s to settle
		if active() {
			time.Sleep(1 * time.Second) // ensure it did not immediately crash
			if active() {
				return nil
			}
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("systemd unit %s is not active after restart (inspect: systemctl status %s ; journalctl -u %s)", systemdUnit, systemdUnit, systemdUnit)
}
