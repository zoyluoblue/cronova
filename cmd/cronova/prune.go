package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zoyluo/cronova/internal/scheduler"
)

// cmdPrune deletes finished runs (and their log directories) older than a
// retention window — the manual counterpart of `serve -retention`, for one-off
// cleanups or deployments that run with retention disabled.
//
//	cronova prune                      # delete finished runs older than 90 days (asks first)
//	cronova prune -older-than 720h     # custom window
//	cronova prune -yes                 # no confirmation (scripts / cron)
func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	configPath := fs.String("config", envOr("CRONOVA_CONFIG", "cronova.yaml"), "path to YAML config file (optional)")
	dbPath := fs.String("db", "", "SQLite metadata database path (default: from config, else data/cronova.db)")
	logDir := fs.String("logs", "", "directory for task log files (default: from config, else logs)")
	olderThan := fs.Duration("older-than", 90*24*time.Hour, "delete finished runs older than this")
	noVacuum := fs.Bool("no-vacuum", false, "skip the VACUUM that returns freed space to the filesystem")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	_ = fs.Parse(args)

	// Resolve db/logs the same way `serve` does (config file, then flags), so
	// a bare `cronova prune` on an installed service prunes the real database
	// instead of silently no-opping against an empty ./data/cronova.db.
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
	if *logDir == "" {
		*logDir = cfg.Logs
	}

	if *olderThan <= 0 {
		return fmt.Errorf("-older-than must be positive (got %s)", olderThan)
	}
	if !*yes && !confirm(fmt.Sprintf(
		"This deletes ALL finished runs older than %s from %s, plus their log directories under %s. Continue?",
		olderThan, *dbPath, *logDir)) {
		fmt.Println("aborted.")
		return nil
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	pruned, err := st.PruneRuns(ctx, time.Now().UTC().Add(-*olderThan))
	if err != nil {
		return err
	}
	var logErrs int
	for _, r := range pruned {
		if err := os.RemoveAll(scheduler.RunLogDir(*logDir, r.DagID, r.RunID)); err != nil {
			logErrs++
		}
	}
	fmt.Printf("pruned %d finished run(s) older than %s", len(pruned), olderThan)
	if logErrs > 0 {
		fmt.Printf(" (%d log dir(s) could not be removed)", logErrs)
	}
	fmt.Println()
	// Deleting rows alone leaves the DB file at its high-water mark; VACUUM
	// rewrites it and returns the space. Only worth the rewrite when rows went.
	if len(pruned) > 0 && !*noVacuum {
		// SQLite only: PostgreSQL's autovacuum manages space on its own.
		if v, ok := st.(interface{ Vacuum(context.Context) error }); ok {
			if err := v.Vacuum(ctx); err != nil {
				return fmt.Errorf("vacuum after prune: %w", err)
			}
			fmt.Println("vacuumed — freed space returned to the filesystem")
		}
	}
	return nil
}
