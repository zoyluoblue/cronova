// Command cronova is the scheduler entrypoint.
//
//	cronova serve              run the scheduling loop (Ctrl-C to stop)
//	cronova trigger <dag_id>   create a manual run (picked up by a running serve)
//	cronova dags               list registered DAGs
//	cronova runs <dag_id>      show recent runs and their task states
package main

import (
	"bufio"
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
	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/scheduler"
	"github.com/zoyluo/cronova/internal/store"
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
	case "users":
		err = cmdUsers(args)
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
  cronova users              list console accounts
  cronova users add <name> -role admin|viewer -password ...   create an account
  cronova users passwd <name> -password ...   change a password
  cronova users delete <name>                 remove an account

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
	configPath := fs.String("config", "cronova.yaml", "path to YAML config file (optional)")
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	logDir := fs.String("logs", "logs", "directory for task log files")
	tick := fs.Duration("tick", 2*time.Second, "scheduling loop interval")
	executorAddr := fs.String("executor", "", "gRPC executor target (e.g. unix:///tmp/cronova-executor.sock); empty = in-process executor")
	httpAddr := fs.String("http", ":8090", "HTTP address for the console API + web UI (empty to disable)")
	authFlag := fs.Bool("auth", false, "require login for the console/API (overrides config)")
	_ = fs.Parse(args)

	// resolve settings: defaults <- config file <- CRONOVA_* env <- explicit flags
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
	overlaySetFlags(&cfg, fs, map[string]any{
		"db": dbPath, "dags": dagDir, "logs": logDir, "tick": tick,
		"executor": executorAddr, "http": httpAddr, "auth": authFlag,
	})

	tickDur, err := time.ParseDuration(cfg.Tick)
	if err != nil || tickDur <= 0 {
		return fmt.Errorf("invalid tick %q: %v", cfg.Tick, err)
	}

	st, err := openStore(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	// seed the initial admin (idempotent) before enabling auth, so a fresh
	// deployment isn't locked out.
	if cfg.Auth.AdminUser != "" && cfg.Auth.AdminPassword != "" {
		if err := seedAdmin(context.Background(), st, cfg.Auth.AdminUser, cfg.Auth.AdminPassword); err != nil {
			return err
		}
	}
	if cfg.Auth.Enabled {
		if n, _ := st.CountUsers(context.Background()); n == 0 {
			log.Print("cronova: WARNING auth enabled but no users exist — create one with 'cronova users add' or set CRONOVA_ADMIN_USER/CRONOVA_ADMIN_PASSWORD")
		}
	} else {
		log.Print("cronova: WARNING authentication is DISABLED — anyone who can reach the console may trigger/delete DAGs and run commands (enable with -auth or auth.enabled)")
	}

	var exec executor.Executor
	executorLabel := "in-process"
	if cfg.Executor != "" {
		client, err := executor.Dial(cfg.Executor)
		if err != nil {
			return err
		}
		defer client.Close()
		exec = client
		executorLabel = cfg.Executor
		log.Printf("cronova: using remote executor at %s", cfg.Executor)
	} else {
		exec = executor.NewLocal()
		log.Print("cronova: using in-process executor (tasks die on restart; use -executor for crash recovery)")
	}

	sch := scheduler.New(st, exec, scheduler.Options{
		DagDir: cfg.Dags,
		LogDir: cfg.Logs,
		Tick:   tickDur,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Console (REST API + web UI) runs in-process alongside the scheduler loop.
	var httpSrv *http.Server
	httpErrCh := make(chan error, 1)
	if cfg.HTTP != "" {
		apiSrv := api.New(st, sch, cfg.Logs, web.FS(), api.Info{Executor: executorLabel, Tick: tickDur.String()})
		apiSrv.SetAuth(api.AuthConfig{Enabled: cfg.Auth.Enabled, SessionTTL: cfg.sessionTTL(), SecureCookie: cfg.Auth.SecureCookie})
		httpSrv = &http.Server{Addr: cfg.HTTP, Handler: apiSrv.Handler()}
		go func() {
			log.Printf("cronova: console on http://%s (auth=%v)", cfg.HTTP, cfg.Auth.Enabled)
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

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// seedAdmin creates an admin account if the username does not yet exist (idempotent).
func seedAdmin(ctx context.Context, st *sqlite.Store, username, password string) error {
	if _, err := st.GetUserByUsername(ctx, username); err == nil {
		return nil // already present
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	log.Printf("cronova: seeding admin account %q", username)
	return st.CreateUser(ctx, &model.User{Username: username, PasswordHash: hash, Role: model.RoleAdmin})
}

// cmdUsers manages console/API accounts: add | list | passwd | delete.
func cmdUsers(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cronova users <add|list|passwd|delete> [name]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	dbPath := fs.String("db", envOr("CRONOVA_DB", "data/cronova.db"), "SQLite metadata database path")
	role := fs.String("role", "viewer", "role for 'add': admin or viewer")
	pw := fs.String("password", "", "password (for add/passwd); if empty, read from stdin")
	pos := parsePositionals(fs, rest)

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	switch sub {
	case "list":
		users, err := st.ListUsers(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "USERNAME\tROLE\tCREATED")
		for _, u := range users {
			fmt.Fprintf(w, "%s\t%s\t%s\n", u.Username, u.Role, u.CreatedAt.Format(time.RFC3339))
		}
		return w.Flush()

	case "add":
		if len(pos) < 1 {
			return fmt.Errorf("usage: cronova users add <name> -role admin|viewer")
		}
		r := model.Role(*role)
		if r != model.RoleAdmin && r != model.RoleViewer {
			return fmt.Errorf("invalid role %q (want admin or viewer)", *role)
		}
		pass, err := resolvePassword(*pw)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(pass)
		if err != nil {
			return err
		}
		if err := st.CreateUser(ctx, &model.User{Username: pos[0], PasswordHash: hash, Role: r}); err != nil {
			return fmt.Errorf("create user (already exists?): %w", err)
		}
		fmt.Printf("created %s account %q\n", r, pos[0])
		return nil

	case "passwd":
		if len(pos) < 1 {
			return fmt.Errorf("usage: cronova users passwd <name>")
		}
		u, err := st.GetUserByUsername(ctx, pos[0])
		if err != nil {
			return fmt.Errorf("user %q: %w", pos[0], err)
		}
		pass, err := resolvePassword(*pw)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(pass)
		if err != nil {
			return err
		}
		if err := st.UpdateUserPassword(ctx, u.ID, hash); err != nil {
			return err
		}
		fmt.Printf("password updated for %q (existing sessions revoked)\n", pos[0])
		return nil

	case "delete":
		if len(pos) < 1 {
			return fmt.Errorf("usage: cronova users delete <name>")
		}
		u, err := st.GetUserByUsername(ctx, pos[0])
		if err != nil {
			return fmt.Errorf("user %q: %w", pos[0], err)
		}
		if err := st.DeleteUser(ctx, u.ID); err != nil {
			return err
		}
		fmt.Printf("deleted account %q\n", pos[0])
		return nil

	default:
		return fmt.Errorf("unknown users subcommand %q (want add|list|passwd|delete)", sub)
	}
}

// resolvePassword returns the flag value, or reads a line from stdin when empty.
func resolvePassword(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	fmt.Fprint(os.Stderr, "password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	pass := strings.TrimRight(line, "\r\n")
	if pass == "" {
		return "", fmt.Errorf("empty password")
	}
	return pass, nil
}
