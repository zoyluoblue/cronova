package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// Service-control constants must match what the installers lay down:
// deploy/cronova.service (systemd) and deploy/com.cronova.plist (launchd).
const (
	systemdUnit  = "cronova"
	launchdLabel = "com.cronova"
	launchdPlist = "/Library/LaunchDaemons/com.cronova.plist"
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

// requireRoot guards mutating actions; status is read-only and skips it.
func requireRoot(action string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("`cronova %s` manages a system service — run: sudo cronova %s", action, action)
	}
	return nil
}

func serviceSystemd(action string) error {
	switch action {
	case "start", "stop", "restart":
		if err := requireRoot(action); err != nil {
			return err
		}
		return run("systemctl", action, systemdUnit)
	case "status":
		return run("systemctl", "--no-pager", "status", systemdUnit)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func serviceLaunchd(action string) error {
	if err := requireRoot(action); err != nil { // launchctl's system domain needs root
		return err
	}
	loaded := exec.Command("launchctl", "print", "system/"+launchdLabel).Run() == nil

	installed := func() error {
		if _, err := os.Stat(launchdPlist); err != nil {
			return fmt.Errorf("service not installed (%s missing) — run the installer, or use `cronova serve` for a local run", launchdPlist)
		}
		return nil
	}

	switch action {
	case "status":
		if !loaded {
			fmt.Println("cronova: not loaded (launchd). Start it with: sudo cronova start")
			return nil
		}
		return run("launchctl", "print", "system/"+launchdLabel)
	case "start":
		if loaded { // already registered — (re)start it if it's not running
			return run("launchctl", "kickstart", "system/"+launchdLabel)
		}
		if err := installed(); err != nil {
			return err
		}
		return run("launchctl", "bootstrap", "system", launchdPlist)
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

// launchdReload unloads (if loaded) and re-bootstraps the daemon. bootout is
// asynchronous, so it waits for the label to disappear before bootstrapping,
// avoiding the race where the new load collides with the still-tearing-down job.
func launchdReload(loaded bool) error {
	if loaded {
		_ = run("launchctl", "bootout", "system/"+launchdLabel) // may already be gone
		for i := 0; i < 40; i++ {                               // ~10s for the async teardown
			if exec.Command("launchctl", "print", "system/"+launchdLabel).Run() != nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	return run("launchctl", "bootstrap", "system", launchdPlist)
}
