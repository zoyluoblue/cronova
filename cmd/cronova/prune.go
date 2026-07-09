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
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	logDir := fs.String("logs", "logs", "directory for task log files")
	olderThan := fs.Duration("older-than", 90*24*time.Hour, "delete finished runs older than this")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	_ = fs.Parse(args)

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
	return nil
}
