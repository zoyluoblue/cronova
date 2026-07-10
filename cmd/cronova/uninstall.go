package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

// cmdUninstall tears down a native cronova install: it stops and removes the
// service and the binaries. Data (config, DB, DAGs, logs) is KEPT by default so
// an uninstall is reversible by re-installing; `--purge` deletes it too.
//
//	cronova uninstall            # remove service + binary, keep data
//	cronova uninstall --purge    # also delete config, DB, DAGs and logs
//	cronova uninstall -yes        # skip the confirmation prompt (scripts)
func cmdUninstall(args []string) error {
	purge, assumeYes := false, false
	for _, a := range args {
		switch a {
		case "--purge", "-purge":
			purge = true
		case "-y", "-yes", "--yes":
			assumeYes = true
		default:
			return fmt.Errorf("unknown flag %q (usage: cronova uninstall [--purge] [-yes])", a)
		}
	}

	if err := ensureRoot("uninstall"); err != nil {
		return err
	}

	if !serviceInstalled() && !binaryInstalled() {
		return errors.New("cronova does not appear to be installed (no service definition and no /usr/local/bin/cronova)")
	}

	if !assumeYes {
		warn := "This stops and removes the cronova service and binary."
		if purge {
			warn = "This stops and removes the cronova service and binary AND PERMANENTLY DELETES all data (config, DB, DAGs, logs)."
		}
		if !confirm(warn + " Continue?") {
			return errors.New("aborted")
		}
	}

	switch runtime.GOOS {
	case "darwin":
		return uninstallDarwin(purge)
	case "linux":
		return uninstallLinux(purge)
	default:
		return fmt.Errorf("uninstall is only supported on macOS/Linux (this is %s)", runtime.GOOS)
	}
}

func binaryInstalled() bool {
	_, err := os.Stat(binDst)
	return err == nil
}

// confirm prints a [y/N] prompt and reads one line from stdin; anything other
// than y/yes (case-insensitive) is a no.
func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	return confirmFrom(os.Stdin)
}

// confirmFrom reads a yes/no answer from r. A final line without a trailing
// newline (Ctrl-D, or `printf 'y'` piping) still counts — the data arrives paired
// with io.EOF, so only a genuinely empty read is treated as "no".
func confirmFrom(r io.Reader) bool {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return false // closed/empty stdin -> safe default: no
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// removePath deletes a file or tree, reporting what happened (best-effort — a
// missing path is fine, since uninstall may run against a partial install).
func removePath(kind, p string) {
	if _, err := os.Stat(p); err != nil {
		return // nothing there
	}
	if err := os.RemoveAll(p); err != nil {
		fmt.Fprintf(os.Stderr, "cronova: warning — could not remove %s %s: %v\n", kind, p, err)
		return
	}
	fmt.Printf("==> removed %s %s\n", kind, p)
}

func uninstallDarwin(purge bool) error {
	// 1. stop + unload the daemon (ignore errors — it may not be loaded).
	if launchdLoaded() {
		fmt.Println("==> stopping launchd daemon")
		_ = run("launchctl", "bootout", "system/"+launchdLabel)
	}
	if launchdJobLoaded(launchdExecutorLabel) {
		fmt.Println("==> stopping launchd executor")
		_ = run("launchctl", "bootout", "system/"+launchdExecutorLabel)
	}
	// 2. service definition + binaries.
	removePath("plist", launchdPlist)
	removePath("plist", launchdExecutorPlist)
	removePath("binary", binExecutor)
	removePath("binary", binDst)

	// 3. data (opt-in).
	if purge {
		removePath("config", "/usr/local/etc/cronova")
		removePath("state", "/usr/local/var/cronova")
		removePath("logs", "/usr/local/var/log/cronova")
	}
	printUninstallSummary(purge, "/usr/local/etc/cronova, /usr/local/var/cronova, /usr/local/var/log/cronova")
	return nil
}

func uninstallLinux(purge bool) error {
	// 1. stop + disable the unit (ignore errors — may be stopped/absent).
	fmt.Println("==> stopping systemd unit")
	_ = run("systemctl", "disable", "--now", systemdUnit)
	_ = run("systemctl", "disable", "--now", systemdExecutorUnit)
	// 2. unit file + reload so systemd forgets it.
	removePath("unit", systemdUnitPath)
	removePath("unit", systemdExecutorUnitPath)
	_ = run("systemctl", "daemon-reload")
	_ = run("systemctl", "reset-failed", systemdUnit)
	_ = run("systemctl", "reset-failed", systemdExecutorUnit)
	// 3. binaries.
	removePath("binary", binExecutor)
	removePath("binary", binDst)

	// 4. data + dedicated user (opt-in).
	if purge {
		removePath("config", "/etc/cronova")
		removePath("state", "/var/lib/cronova")
		removePath("logs", "/var/log/cronova")
		if err := run("userdel", "cronova"); err != nil {
			fmt.Fprintln(os.Stderr, "cronova: note — could not remove the 'cronova' system user (may not exist)")
		} else {
			fmt.Println("==> removed system user cronova")
		}
	}
	printUninstallSummary(purge, "/etc/cronova, /var/lib/cronova, /var/log/cronova")
	return nil
}

func printUninstallSummary(purged bool, dataDirs string) {
	fmt.Println()
	if purged {
		fmt.Println("cronova fully removed.")
	} else {
		fmt.Println("cronova service and binary removed. Data kept at:")
		fmt.Printf("  %s\n", dataDirs)
		fmt.Println("(the binary is gone, so delete those dirs by hand, or re-run --purge before uninstalling next time.)")
	}
}
