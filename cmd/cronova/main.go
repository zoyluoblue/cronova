// Command cronova is the scheduler entrypoint.
//
//	cronova serve              run the scheduling loop (Ctrl-C to stop)
//	cronova trigger <dag_id>   create a manual run (picked up by a running serve)
//	cronova dags               list registered DAGs
//	cronova runs <dag_id>      show recent runs and their task states
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/zoyluo/cronova/internal/api"
	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/scheduler"
	"github.com/zoyluo/cronova/internal/store/sqlite"
	"github.com/zoyluo/cronova/internal/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "serve":
		err = cmdServe(args)
	case "trigger":
		err = cmdTrigger(args)
	case "dags":
		err = cmdDags(args)
	case "runs":
		err = cmdRuns(args)
	case "pools":
		err = cmdPools(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("cronova %s: %v", cmd, err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `cronova - a workflow scheduler

usage:
  cronova serve              run the scheduling loop
  cronova trigger <dag_id>   create a manual run
  cronova dags               list registered DAGs
  cronova runs <dag_id>      show recent runs and task states
  cronova pools              list resource pools
  cronova pools set <name> <slots>   create or resize a pool

run "cronova <command> -h" for command flags
`)
}

// parsePositionals parses flags that may appear before or after the leading
// positional arguments, returning the positionals. Works around Go's flag
// package stopping at the first non-flag token.
func parsePositionals(fs *flag.FlagSet, args []string) []string {
	_ = fs.Parse(args)
	rest := fs.Args()
	var pos []string
	i := 0
	for i < len(rest) && !strings.HasPrefix(rest[i], "-") {
		pos = append(pos, rest[i])
		i++
	}
	if i < len(rest) {
		_ = fs.Parse(rest[i:])
	}
	return pos
}

func openStore(dbPath string) (*sqlite.Store, error) {
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	st, err := sqlite.New(dbPath)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	logDir := fs.String("logs", "logs", "directory for task log files")
	tick := fs.Duration("tick", 2*time.Second, "scheduling loop interval")
	executorAddr := fs.String("executor", "", "gRPC executor target (e.g. unix:///tmp/cronova-executor.sock); empty = in-process executor")
	httpAddr := fs.String("http", ":8090", "HTTP address for the console API + web UI (empty to disable)")
	_ = fs.Parse(args)

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	var exec executor.Executor
	executorLabel := "in-process"
	if *executorAddr != "" {
		client, err := executor.Dial(*executorAddr)
		if err != nil {
			return err
		}
		defer client.Close()
		exec = client
		executorLabel = *executorAddr
		log.Printf("cronova: using remote executor at %s", *executorAddr)
	} else {
		exec = executor.NewLocal()
		log.Print("cronova: using in-process executor (tasks die on restart; use -executor for crash recovery)")
	}

	sch := scheduler.New(st, exec, scheduler.Options{
		DagDir: *dagDir,
		LogDir: *logDir,
		Tick:   *tick,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Console (REST API + web UI) runs in-process alongside the scheduler loop.
	var httpSrv *http.Server
	httpErrCh := make(chan error, 1)
	if *httpAddr != "" {
		apiSrv := api.New(st, sch, *logDir, web.FS(), api.Info{Executor: executorLabel, Tick: tick.String()})
		httpSrv = &http.Server{Addr: *httpAddr, Handler: apiSrv.Handler()}
		go func() {
			log.Printf("cronova: console on http://%s", *httpAddr)
			err := httpSrv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("cronova: http server error: %v", err)
				httpErrCh <- err
				cancel() // bring the scheduler down too
			}
		}()
	}

	runErr := sch.Run(ctx)

	if httpSrv != nil {
		shutCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = httpSrv.Shutdown(shutCtx)
	}
	// An HTTP startup failure (e.g. port in use) must surface as a non-zero exit,
	// distinct from a clean SIGINT shutdown (which cancels with context.Canceled).
	select {
	case herr := <-httpErrCh:
		return herr
	default:
	}
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}

func cmdTrigger(args []string) error {
	fs := flag.NewFlagSet("trigger", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	pos := parsePositionals(fs, args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova trigger <dag_id>")
	}
	dagID := pos[0]

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	sch := scheduler.New(st, executor.NewLocal(), scheduler.Options{DagDir: *dagDir})
	ctx := context.Background()
	if err := sch.LoadDAGs(ctx); err != nil {
		return err
	}
	runID, err := sch.TriggerManual(ctx, dagID)
	if err != nil {
		return err
	}
	fmt.Printf("created run %s (a running `cronova serve` will execute it)\n", runID)
	return nil
}

func cmdDags(args []string) error {
	fs := flag.NewFlagSet("dags", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	_ = fs.Parse(args)

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Load from disk so freshly-added DAGs show up even before serve runs.
	sch := scheduler.New(st, executor.NewLocal(), scheduler.Options{DagDir: *dagDir})
	if err := sch.LoadDAGs(context.Background()); err != nil {
		return err
	}

	dags, err := st.ListDAGs(context.Background())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "DAG_ID\tSCHEDULE\tCATCHUP\tPAUSED\tMAX_ACTIVE")
	for _, d := range dags {
		sched := d.Schedule
		if sched == "" {
			sched = "(manual)"
		}
		fmt.Fprintf(w, "%s\t%s\t%t\t%t\t%d\n", d.DagID, sched, d.Catchup, d.Paused, d.MaxActiveRuns)
	}
	return w.Flush()
}

func cmdPools(args []string) error {
	fs := flag.NewFlagSet("pools", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	pos := parsePositionals(fs, args)

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	if len(pos) > 0 && pos[0] == "set" {
		if len(pos) != 3 {
			return fmt.Errorf("usage: cronova pools set <name> <slots>")
		}
		slots, err := strconv.Atoi(pos[2])
		if err != nil || slots <= 0 {
			return fmt.Errorf("slots must be a positive integer, got %q", pos[2])
		}
		if err := st.UpsertPool(ctx, &model.Pool{Name: pos[1], Slots: slots}); err != nil {
			return err
		}
		fmt.Printf("pool %q set to %d slots\n", pos[1], slots)
		return nil
	}

	pools, err := st.ListPools(ctx)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSLOTS")
	for _, p := range pools {
		fmt.Fprintf(w, "%s\t%d\n", p.Name, p.Slots)
	}
	return w.Flush()
}

func cmdRuns(args []string) error {
	fs := flag.NewFlagSet("runs", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	limit := fs.Int("n", 10, "number of recent runs to show")
	pos := parsePositionals(fs, args)
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova runs <dag_id>")
	}
	dagID := pos[0]

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	runs, err := st.ListDagRuns(ctx, dagID, *limit)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RUN_ID\tLOGICAL_DATE\tSTATE\tTRIGGER\tTASKS")
	for _, r := range runs {
		tis, err := st.ListTaskInstances(ctx, r.RunID)
		if err != nil {
			return err
		}
		summary := ""
		for _, ti := range tis {
			summary += fmt.Sprintf("%s=%s ", ti.TaskID, ti.State)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			r.RunID, r.LogicalDate.Format(time.RFC3339), r.State, r.TriggerType, summary)
	}
	return w.Flush()
}
