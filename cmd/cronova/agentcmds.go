package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/client"
	"github.com/zoyluo/cronova/internal/model"
)

// Operator verbs for agents/scripts. These map 1:1 to REST endpoints and are
// remote-only (they need a running server + token) — the friendly face of
// `cronova api`. Reads print the JSON response; writes print the result.

func cmdGet(args []string) error { // get a DAG definition
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova get <dag_id>")
	}
	return callAndEmit(g, "GET", "/api/dags/{id}", client.Options{Path: map[string]string{"id": pos[0]}})
}

func cmdRun(args []string) error { // get one run with task states
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova run <run_id>")
	}
	return callAndEmit(g, "GET", "/api/runs/{runID}", client.Options{Path: map[string]string{"runID": pos[0]}})
}

func cmdLogs(args []string) error { // fetch a task instance's log (plain text)
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova logs <task_instance_id>")
	}
	return callAndEmit(g, "GET", "/api/tasks/{tiID}/log", client.Options{Path: map[string]string{"tiID": pos[0]}, Accept: "text/plain"})
}

func cmdCancel(args []string) error { // cancel an active run
	fs := flag.NewFlagSet("cancel", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova cancel <run_id>")
	}
	return callAndEmit(g, "POST", "/api/runs/{runID}/cancel", client.Options{Path: map[string]string{"runID": pos[0]}})
}

func cmdRetry(args []string) error { // retry a whole run's failed tasks, or one task
	fs := flag.NewFlagSet("retry", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	switch len(pos) {
	case 1:
		return callAndEmit(g, "POST", "/api/runs/{runID}/retry", client.Options{Path: map[string]string{"runID": pos[0]}})
	case 2:
		return callAndEmit(g, "POST", "/api/runs/{runID}/tasks/{taskID}/retry",
			client.Options{Path: map[string]string{"runID": pos[0], "taskID": pos[1]}})
	default:
		return fmt.Errorf("usage: cronova retry <run_id> [task_id]")
	}
}

func cmdMark(args []string) error { // operator override: mark a run or task state
	fs := flag.NewFlagSet("mark", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	// mark <run_id> <state>            -> mark the run (success|failed)
	// mark <run_id> <task_id> <state>  -> mark one task (success|failed|skipped)
	switch len(pos) {
	case 2:
		body, _ := json.Marshal(map[string]string{"state": pos[1]})
		return callAndEmit(g, "POST", "/api/runs/{runID}/mark", client.Options{Path: map[string]string{"runID": pos[0]}, Body: body})
	case 3:
		body, _ := json.Marshal(map[string]string{"state": pos[2]})
		return callAndEmit(g, "POST", "/api/runs/{runID}/tasks/{taskID}/mark",
			client.Options{Path: map[string]string{"runID": pos[0], "taskID": pos[1]}, Body: body})
	default:
		return fmt.Errorf("usage: cronova mark <run_id> [task_id] <state>   (run: success|failed; task: success|failed|skipped)")
	}
}

func cmdPause(args []string) error { // pause/resume a DAG's scheduling
	fs := flag.NewFlagSet("pause", flag.ExitOnError)
	off := fs.Bool("off", false, "resume (unpause) instead of pausing")
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) == 0 {
		return fmt.Errorf("usage: cronova pause <dag_id> [-off]")
	}
	paused := "true"
	if *off {
		paused = "false"
	}
	return callAndEmit(g, "POST", "/api/dags/{id}/pause",
		client.Options{Path: map[string]string{"id": pos[0]}, Query: map[string]string{"paused": paused}})
}

func cmdOverview(args []string) error { // dashboard summary (DAGs, active runs, pools)
	fs := flag.NewFlagSet("overview", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	_ = fs.Parse(args)
	return callAndEmit(resolve(), "GET", "/api/overview", client.Options{})
}

// cmdTokens provisions and manages API tokens. `create` writes directly to the
// local store (the bootstrap path — you need a token to reach the API, so the
// first one can't come from the API). list/delete are local too, since token
// admin is a server-host operation.
//
//	cronova tokens create <name> [-role admin|viewer]
//	cronova tokens list
//	cronova tokens delete <id>
func cmdTokens(args []string) error {
	fs := flag.NewFlagSet("tokens", flag.ExitOnError)
	dbPath := fs.String("db", "data/cronova.db", "SQLite metadata database path")
	role := fs.String("role", "admin", "token role for create: admin or viewer")
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()

	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	action := ""
	if len(pos) > 0 {
		action = pos[0]
	}
	switch action {
	case "create":
		if len(pos) < 2 {
			return fmt.Errorf("usage: cronova tokens create <name> [-role admin|viewer]")
		}
		r := model.Role(*role)
		if r != model.RoleAdmin && r != model.RoleViewer {
			return fmt.Errorf("role must be admin or viewer, got %q", *role)
		}
		plaintext, hash, err := auth.NewAPIToken()
		if err != nil {
			return fmt.Errorf("token generation failed: %w", err)
		}
		tok := &model.APIToken{Name: pos[1], Role: r, Prefix: plaintext[:len(auth.APITokenPrefix)+6]}
		if err := st.CreateAPIToken(ctx, tok, hash); err != nil {
			return err
		}
		tok.Plaintext = plaintext
		if g.asJSON() {
			return printJSON(tok)
		}
		fmt.Printf("created %s token %q\n\n  %s\n\nStore it now — it is not shown again. Use it with:\n  export CRONOVA_TOKEN=%s\n",
			r, tok.Name, plaintext, plaintext)
		return nil

	case "delete", "rm":
		if len(pos) < 2 {
			return fmt.Errorf("usage: cronova tokens delete <id>")
		}
		id, err := strconv.ParseInt(pos[1], 10, 64)
		if err != nil {
			return fmt.Errorf("token id must be a number, got %q", pos[1])
		}
		if err := st.DeleteAPIToken(ctx, id); err != nil {
			return err
		}
		fmt.Printf("deleted token %d\n", id)
		return nil

	case "", "list", "ls":
		toks, err := st.ListAPITokens(ctx)
		if err != nil {
			return err
		}
		if g.asJSON() {
			if toks == nil {
				toks = []*model.APIToken{}
			}
			return printJSON(toks)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tROLE\tPREFIX\tLAST_USED")
		for _, t := range toks {
			last := "never"
			if t.LastUsedAt != nil {
				last = t.LastUsedAt.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%d\t%s\t%s\t%s…\t%s\n", t.ID, t.Name, t.Role, t.Prefix, last)
		}
		return w.Flush()

	default:
		return fmt.Errorf("unknown tokens action %q (create|list|delete)", action)
	}
}
