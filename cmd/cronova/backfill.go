package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/scheduler"
)

// cmdBackfill enqueues runs for every schedule period in a date window — the
// CLI counterpart of POST /api/dags/{id}/backfill (use `cronova api` for a
// remote server).
//
//	cronova backfill daily_etl -from 2026-07-01 -to 2026-07-05
func cmdBackfill(args []string) error {
	fs := flag.NewFlagSet("backfill", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	from := fs.String("from", "", "window start, YYYY-MM-DD (inclusive)")
	to := fs.String("to", "", "window end, YYYY-MM-DD (inclusive)")
	pos := parsePositionals(fs, args)
	if len(pos) == 0 || *from == "" || *to == "" {
		return fmt.Errorf("usage: cronova backfill <dag_id> -from YYYY-MM-DD -to YYYY-MM-DD")
	}
	fromT, err := time.Parse("2006-01-02", *from)
	if err != nil {
		return fmt.Errorf("invalid -from %q: %w", *from, err)
	}
	toT, err := time.Parse("2006-01-02", *to)
	if err != nil {
		return fmt.Errorf("invalid -to %q: %w", *to, err)
	}
	toT = toT.Add(24*time.Hour - time.Second) // inclusive end date

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	sch := scheduler.New(st, executor.NewLocal(), scheduler.Options{DagDir: *dagDir})
	if err := sch.LoadDAGs(ctx); err != nil {
		return err
	}
	created, skipped, err := sch.Backfill(ctx, pos[0], fromT.UTC(), toT.UTC())
	if err != nil {
		return err
	}
	fmt.Printf("backfill %s: created %d run(s), skipped %d existing (a running `cronova serve` executes them)\n",
		pos[0], created, skipped)
	return nil
}
