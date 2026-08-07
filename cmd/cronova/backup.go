package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// cmdBackup takes a consistent snapshot of everything a restore needs:
//
//	cronova backup <dest-dir>            # snapshot DB + key + dags/ + projects/
//	cronova backup -config /etc/... d/  # resolve paths from an installed config
//
// The database is copied with VACUUM INTO (safe and atomic on a LIVE server —
// no need to stop `cronova serve`), unlike a raw cp which can capture a torn
// mid-commit state. The encryption key, DAG YAML directory, and uploaded
// projects ride along because a DB restored without them is incomplete:
// connection passwords become unreadable without the key.
func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	configPath := fs.String("config", envOr("CRONOVA_CONFIG", "cronova.yaml"), "path to YAML config file (optional)")
	dbPath := fs.String("db", "", "SQLite metadata database path (default: from config)")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cronova backup [-config file] <dest-dir>")
	}
	dest := fs.Arg(0)

	cfg := defaultConfig()
	configExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})
	if err := loadConfigFile(&cfg, *configPath, configExplicit); err != nil {
		return err
	}
	applyEnv(&cfg)
	if *dbPath == "" {
		*dbPath = cfg.DB
	}
	if cfg.Projects == "" {
		cfg.Projects = defaultProjectsDir()
	}

	if _, err := os.Stat(*dbPath); err != nil {
		return fmt.Errorf("database %s not found (use -config or -db to point at the installed instance): %w", *dbPath, err)
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	destDB := filepath.Join(dest, "cronova.db")
	if _, err := os.Stat(destDB); err == nil {
		return fmt.Errorf("%s already exists — back up into a fresh directory", destDB)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := st.BackupTo(ctx, destDB); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	fmt.Printf("database  → %s\n", destDB)

	// The key file is the difference between a backup and a paperweight: without
	// it, encrypted connection passwords in the snapshot are unreadable.
	if cfg.KeyFile != "" && cfg.KeyFile != "none" {
		if err := copyFileIfExists(cfg.KeyFile, filepath.Join(dest, "cronova.key"), 0o600); err != nil {
			return fmt.Errorf("copy key file: %w", err)
		}
	}
	if err := copyDirIfExists(cfg.Dags, filepath.Join(dest, "dags")); err != nil {
		return fmt.Errorf("copy dags dir: %w", err)
	}
	if err := copyDirIfExists(cfg.Projects, filepath.Join(dest, "projects")); err != nil {
		return fmt.Errorf("copy projects dir: %w", err)
	}

	fmt.Printf("backup complete: %s\n", dest)
	fmt.Println("restore: stop cronova, place cronova.db/cronova.key/dags/projects back at their configured paths, start cronova, then `cronova healthcheck`.")
	return nil
}

func copyFileIfExists(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("key file  → %s\n", dst)
	return os.WriteFile(dst, b, mode)
}

func copyDirIfExists(src, dst string) error {
	info, err := os.Stat(src)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		return err
	}
	fmt.Printf("directory → %s\n", dst)
	return nil
}
