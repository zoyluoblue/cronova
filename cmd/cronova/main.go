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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/zoyluo/cronova/internal/api"
	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/client"
	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/operator"
	"github.com/zoyluo/cronova/internal/scheduler"
	"github.com/zoyluo/cronova/internal/secrets"
	"github.com/zoyluo/cronova/internal/store"
	"github.com/zoyluo/cronova/internal/store/sqlite"
	"github.com/zoyluo/cronova/internal/web"
)

// version is the build's release version, injected at link time via
// -ldflags "-X main.version=…" by the Makefile / scripts/package.sh. It is "dev"
// for a plain `go build`. `cronova update` compares against it and reports it.
var version = "dev"

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
	case "prune":
		err = cmdPrune(args)
	case "backfill":
		err = cmdBackfill(args)
	case "api":
		err = cmdAPI(args)
	case "get":
		err = cmdGet(args)
	case "run":
		err = cmdRun(args)
	case "logs":
		err = cmdLogs(args)
	case "cancel":
		err = cmdCancel(args)
	case "retry":
		err = cmdRetry(args)
	case "mark":
		err = cmdMark(args)
	case "pause":
		err = cmdPause(args)
	case "overview":
		err = cmdOverview(args)
	case "tokens":
		err = cmdTokens(args)
	case "mcp":
		err = cmdMCP(args)
	case "users":
		err = cmdUsers(args)
	case "init":
		err = cmdInit(args)
	case "start", "stop", "restart", "status":
		err = cmdService(cmd)
	case "update":
		err = cmdUpdate(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "version", "--version", "-v":
		err = cmdVersion()
	case "healthcheck":
		err = cmdHealthcheck(args)
	case "run-op":
		err = cmdRunOp(args)
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

// cmdRunOp executes a typed task operator in this subprocess. It is invoked
// internally by the scheduler ("<cronova> run-op <type>") for non-shell task
// types; the operator spec (with templates already resolved) arrives as JSON in
// CRONOVA_OP_SPEC. Output goes to stdout (the task log); the exit code drives the
// task's success/retry, so a request failure exits non-zero rather than erroring.
func cmdRunOp(args []string) error {
	if len(args) < 1 {
		return errors.New("run-op requires an operator type (e.g. http)")
	}
	blob := os.Getenv("CRONOVA_OP_SPEC")
	switch args[0] {
	case "http":
		var spec model.HTTPSpec
		if err := json.Unmarshal([]byte(blob), &spec); err != nil {
			return fmt.Errorf("bad op spec: %w", err)
		}
		os.Exit(operator.RunHTTP(context.Background(), spec, os.Stdout))
	case "python":
		var spec struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(blob), &spec); err != nil {
			return fmt.Errorf("bad op spec: %w", err)
		}
		os.Exit(operator.RunPython(context.Background(), spec.Code, os.Stdout))
	case "sql":
		var spec operator.SQLSpec
		if err := json.Unmarshal([]byte(blob), &spec); err != nil {
			return fmt.Errorf("bad op spec: %w", err)
		}
		os.Exit(operator.RunSQL(context.Background(), spec, os.Stdout))
	default:
		return fmt.Errorf("unknown operator type %q", args[0])
	}
	return nil
}

// cmdVersion prints the build version and platform (the release asset `update`
// would fetch for this host).
func cmdVersion() error {
	fmt.Printf("cronova %s %s/%s\n", version, runtime.GOOS, runtime.GOARCH)
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `cronova - a workflow scheduler

usage:
  cronova serve              run the scheduling loop
  cronova trigger <dag_id>   create a manual run
  cronova dags               list registered DAGs
  cronova runs <dag_id>      show recent runs and task states
  cronova backfill <dag_id> -from YYYY-MM-DD -to YYYY-MM-DD   enqueue runs for missed periods
  cronova pools              list resource pools
  cronova pools set <name> <slots>   create or resize a pool
  cronova users              list console accounts
  cronova users add <name> -role admin|viewer -password ...   create an account
  cronova users passwd <name> -password ...   change a password
  cronova users delete <name>                 remove an account
  cronova init               interactive first-time setup (port/bind/admin/auth)

  --- agent / remote mode (set -server + -token, or CRONOVA_SERVER/CRONOVA_TOKEN; add -o json) ---
  cronova api <METHOD> <path> [json-body]     raw call to any REST endpoint
  cronova get <dag_id>                        show a DAG definition
  cronova run <run_id>                        show a run + its task states
  cronova logs <task_instance_id>             fetch a task's log
  cronova cancel <run_id>                     cancel an active run
  cronova retry <run_id> [task_id]            retry a run's failed tasks (or one task)
  cronova mark <run_id> [task_id] <state>     operator override of a run/task state
  cronova pause <dag_id> [-off]               pause or resume scheduling
  cronova overview                            dashboard summary (DAGs/runs/pools)
  cronova tokens create <name> [-role ...]    mint an API token for an agent (local)
  cronova tokens list | delete <id>           manage API tokens (local)
  cronova mcp [-read-only]                     run an MCP server (stdio) for AI clients
  cronova prune [-older-than 2160h] [-yes]    delete finished runs + logs older than the window

  cronova start|stop|restart control the installed service (auto-elevates via sudo)
  cronova status             show the installed service's status
  cronova update [version] [-proxy URL]   download + install the latest (or given) release, then restart
  cronova uninstall [--purge] remove the service + binary (--purge also deletes data)
  cronova version            print the build version and platform
  cronova healthcheck        probe /readyz and exit non-zero if unhealthy

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

// workspaceDirFor derives a stable, per-deployment workspace root from the DB
// path, so concurrent cronova instances on one host stay isolated (each GC only
// sees its own workspaces). Same DB across restarts -> same dir, which is what
// lets crash recovery re-attach to a still-staged workspace.
func workspaceDirFor(dbPath string) string {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		abs = dbPath
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(os.TempDir(), fmt.Sprintf("cronova-ws-%x", sum[:6]))
}

// defaultProjectsDir resolves ~/.cronova/projects for the service user. It tries
// $HOME first, then the user record (launchd daemons may run without $HOME set),
// and returns "" if no home can be determined (the projects feature is then off
// unless -projects/CRONOVA_PROJECTS is given explicitly).
func defaultProjectsDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cronova", "projects")
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return filepath.Join(u.HomeDir, ".cronova", "projects")
	}
	return ""
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "cronova.yaml", "path to YAML config file (optional)")
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	logDir := fs.String("logs", "logs", "directory for task log files")
	projectsDir := fs.String("projects", "", "directory for uploaded project files (default ~/.cronova/projects)")
	tick := fs.Duration("tick", 2*time.Second, "scheduling loop interval")
	executorAddr := fs.String("executor", "", "gRPC executor target (e.g. unix:///tmp/cronova-executor.sock); empty = in-process executor")
	httpAddr := fs.String("http", ":8090", "HTTP address for the console API + web UI (empty to disable)")
	authFlag := fs.Bool("auth", false, "require login for the console/API (overrides config)")
	retention := fs.Duration("retention", 90*24*time.Hour, "delete finished runs + their logs older than this (0 = keep forever)")
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
		"db": dbPath, "dags": dagDir, "logs": logDir, "projects": projectsDir, "tick": tick,
		"executor": executorAddr, "http": httpAddr, "auth": authFlag, "retention": retention,
	})
	if cfg.Projects == "" {
		cfg.Projects = defaultProjectsDir() // ~/.cronova/projects (may be "" if no home)
	}

	tickDur, err := time.ParseDuration(cfg.Tick)
	if err != nil || tickDur <= 0 {
		return fmt.Errorf("invalid tick %q: %v", cfg.Tick, err)
	}
	retentionDur, err := parseRetention(cfg.Retention)
	if err != nil {
		return err
	}

	st, err := openStore(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	// At-rest encryption for connection passwords: load (or mint) the key file
	// and seal any legacy plaintext rows. "none" opts out explicitly.
	if cfg.KeyFile != "" && cfg.KeyFile != "none" {
		key, created, err := secrets.LoadOrCreateKeyFile(cfg.KeyFile)
		if err != nil {
			return fmt.Errorf("encryption key: %w", err)
		}
		cip, err := secrets.NewCipher(key)
		if err != nil {
			return err
		}
		st.SetSecretCipher(cip)
		if created {
			log.Printf("cronova: generated connection-encryption key %s — back this file up; losing it makes stored connection passwords unreadable", cfg.KeyFile)
		}
		if n, err := st.MigrateConnectionSecrets(context.Background()); err != nil {
			return fmt.Errorf("encrypt existing connections: %w", err)
		} else if n > 0 {
			log.Printf("cronova: encrypted %d existing connection password(s) at rest", n)
		}
	} else {
		log.Print("cronova: WARNING connection-password encryption disabled (key_file: none) — passwords are stored in plaintext")
	}

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

	if cfg.Projects != "" {
		if err := os.MkdirAll(cfg.Projects, 0o755); err != nil {
			return fmt.Errorf("create projects dir %s: %w", cfg.Projects, err)
		}
	}

	sch := scheduler.New(st, exec, scheduler.Options{
		DagDir:      cfg.Dags,
		LogDir:      cfg.Logs,
		ProjectsDir: cfg.Projects,
		// Per-DB workspace dir: two cronova instances on one host (e.g. an
		// installed service + a dev run) must not GC each other's workspaces.
		WorkspaceDir: workspaceDirFor(cfg.DB),
		Tick:         tickDur,
		Retention:    retentionDur,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Console (REST API + web UI) runs in-process alongside the scheduler loop.
	var httpSrv *http.Server
	httpErrCh := make(chan error, 1)
	if cfg.HTTP != "" {
		apiSrv := api.New(st, sch, cfg.Logs, web.FS(), api.Info{Executor: executorLabel, Tick: tickDur.String()})
		apiSrv.SetAuth(api.AuthConfig{Enabled: cfg.Auth.Enabled, SessionTTL: cfg.sessionTTL(), SecureCookie: cfg.Auth.SecureCookie})
		apiSrv.SetProjectsDir(cfg.Projects)
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
	paramsJSON := fs.String("params", "", `trigger params as a JSON object, e.g. '{"day":"2026-01-01"}'`)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova trigger <dag_id> [-params '{...}']")
	}
	dagID := pos[0]
	ctx := context.Background()

	var params map[string]string
	if *paramsJSON != "" {
		if err := json.Unmarshal([]byte(*paramsJSON), &params); err != nil {
			return fmt.Errorf("-params must be a JSON object of string values: %w", err)
		}
	}

	if g.remote() {
		c, err := g.client()
		if err != nil {
			return err
		}
		runID, err := c.TriggerDAG(ctx, dagID, params)
		if err != nil {
			return err
		}
		if g.asJSON() {
			return printJSON(map[string]string{"run_id": runID})
		}
		fmt.Printf("created run %s\n", runID)
		return nil
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	sch := scheduler.New(st, executor.NewLocal(), scheduler.Options{DagDir: *dagDir})
	if err := sch.LoadDAGs(ctx); err != nil {
		return err
	}
	runID, err := sch.TriggerManual(ctx, dagID, params)
	if err != nil {
		return err
	}
	if g.asJSON() {
		return printJSON(map[string]string{"run_id": runID})
	}
	fmt.Printf("created run %s (a running `cronova serve` will execute it)\n", runID)
	return nil
}

func cmdDags(args []string) error {
	fs := flag.NewFlagSet("dags", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	dagDir := fs.String("dags", "dags", "directory of DAG YAML definitions")
	resolve := addGlobalFlags(fs)
	_ = fs.Parse(args)
	g := resolve()
	ctx := context.Background()

	var dags []model.DAG
	if g.remote() {
		c, err := g.client()
		if err != nil {
			return err
		}
		if dags, err = c.ListDAGs(ctx); err != nil {
			return err
		}
	} else {
		st, err := openStore(*dbPath)
		if err != nil {
			return err
		}
		defer st.Close()
		// Load from disk so freshly-added DAGs show up even before serve runs.
		sch := scheduler.New(st, executor.NewLocal(), scheduler.Options{DagDir: *dagDir})
		if err := sch.LoadDAGs(ctx); err != nil {
			return err
		}
		ptrs, err := st.ListDAGs(ctx)
		if err != nil {
			return err
		}
		for _, d := range ptrs {
			d.DefinitionYAML = ""
			dags = append(dags, *d)
		}
	}

	if g.asJSON() {
		if dags == nil {
			dags = []model.DAG{}
		}
		return printJSON(dags)
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
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	ctx := context.Background()

	isSet := len(pos) > 0 && pos[0] == "set"
	if isSet && len(pos) != 3 {
		return fmt.Errorf("usage: cronova pools set <name> <slots>")
	}

	// Remote mode: list/set via the API.
	if g.remote() {
		c, err := g.client()
		if err != nil {
			return err
		}
		if isSet {
			slots, err := strconv.Atoi(pos[2])
			if err != nil || slots <= 0 {
				return fmt.Errorf("slots must be a positive integer, got %q", pos[2])
			}
			// POST /api/pools/{name} takes slots as a QUERY param, not a body.
			if _, err := c.Call(ctx, "POST", "/api/pools/{name}", client.Options{
				Path:  map[string]string{"name": pos[1]},
				Query: map[string]string{"slots": strconv.Itoa(slots)},
			}); err != nil {
				return err
			}
			fmt.Printf("pool %q set to %d slots\n", pos[1], slots)
			return nil
		}
		pools, err := c.ListPools(ctx)
		if err != nil {
			return err
		}
		if g.asJSON() {
			if pools == nil {
				pools = []model.Pool{}
			}
			return printJSON(pools)
		}
		return renderPools(pools)
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if isSet {
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

	ptrs, err := st.ListPools(ctx)
	if err != nil {
		return err
	}
	pools := make([]model.Pool, 0, len(ptrs))
	for _, p := range ptrs {
		pools = append(pools, *p)
	}
	if g.asJSON() {
		return printJSON(pools)
	}
	return renderPools(pools)
}

func renderPools(pools []model.Pool) error {
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
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova runs <dag_id>")
	}
	dagID := pos[0]
	ctx := context.Background()

	// Remote: the runs endpoint returns runs only (no per-task states), so print
	// them as JSON or a compact table without the tasks column.
	if g.remote() {
		c, err := g.client()
		if err != nil {
			return err
		}
		runs, err := c.ListRuns(ctx, dagID, *limit)
		if err != nil {
			return err
		}
		if g.asJSON() {
			if runs == nil {
				runs = []model.DagRun{}
			}
			return printJSON(runs)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "RUN_ID\tLOGICAL_DATE\tSTATE\tTRIGGER")
		for _, r := range runs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.RunID, r.LogicalDate.Format(time.RFC3339), r.State, r.TriggerType)
		}
		return w.Flush()
	}

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	runs, err := st.ListDagRuns(ctx, dagID, *limit)
	if err != nil {
		return err
	}
	if g.asJSON() {
		type runOut struct {
			*model.DagRun
			Tasks []*model.TaskInstance `json:"tasks"`
		}
		out := []runOut{}
		for _, r := range runs {
			tis, err := st.ListTaskInstances(ctx, r.RunID)
			if err != nil {
				return err
			}
			out = append(out, runOut{DagRun: r, Tasks: tis})
		}
		return printJSON(out)
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

// cmdHealthcheck probes /readyz over HTTP and exits non-zero if unhealthy. It
// lets an external supervisor (systemd, a load balancer, a cron probe) check
// liveness using the cronova binary itself, with no curl/shell dependency.
func cmdHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ExitOnError)
	addr := fs.String("http", envOr("CRONOVA_HTTP", ":8090"), "server HTTP address")
	path := fs.String("path", "/readyz", "path to probe")
	_ = fs.Parse(args)
	host := *addr
	if strings.HasPrefix(host, ":") {
		host = "localhost" + host
	}
	host = strings.Replace(host, "0.0.0.0", "127.0.0.1", 1)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + host + *path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	return nil
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
