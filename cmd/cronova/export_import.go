package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/zoyluo/cronova/internal/client"
)

// cmdExport writes a portable bundle of an instance's configuration:
//
//	cronova export <dest-dir> -server http://host:8090 -token $TOKEN
//
// dest/dags/*.yaml (full definitions), dest/pools.json, dest/variables.json,
// dest/connections.json, dest/alert-groups.json. Connection PASSWORDS are
// deliberately excluded (the
// API never returns them); after import they must be re-entered — a bundle you
// can email must not carry secrets. Run history and logs are not exported
// (that is `cronova backup`'s job).
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) != 1 {
		return fmt.Errorf("usage: cronova export <dest-dir> (needs -server/-token or CRONOVA_SERVER/CRONOVA_TOKEN)")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	dest := pos[0]
	if err := os.MkdirAll(filepath.Join(dest, "dags"), 0o700); err != nil {
		return err
	}

	var list []struct {
		DagID string `json:"dag_id"`
	}
	if _, err := c.CallJSON(ctx, "GET", "/api/dags", client.Options{}, &list); err != nil {
		return fmt.Errorf("list dags: %w", err)
	}
	for _, d := range list {
		var full struct {
			DefinitionYAML string `json:"definition_yaml"`
		}
		if _, err := c.CallJSON(ctx, "GET", "/api/dags/{id}", client.Options{Path: map[string]string{"id": d.DagID}}, &full); err != nil {
			return fmt.Errorf("export dag %s: %w", d.DagID, err)
		}
		if err := os.WriteFile(filepath.Join(dest, "dags", d.DagID+".yaml"), []byte(full.DefinitionYAML), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("dags        → %d file(s)\n", len(list))

	for name, path := range map[string]string{"pools.json": "/api/pools", "variables.json": "/api/variables", "connections.json": "/api/connections", "alert-groups.json": "/api/alert-groups"} {
		var raw json.RawMessage
		if _, err := c.CallJSON(ctx, "GET", path, client.Options{}, &raw); err != nil {
			return fmt.Errorf("export %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dest, name), raw, 0o600); err != nil {
			return err
		}
		fmt.Printf("%-11s → %s\n", name[:len(name)-5], filepath.Join(dest, name))
	}
	fmt.Println("export complete (connection passwords are NOT included — re-enter them after import)")
	return nil
}

// cmdImport pushes a bundle produced by `cronova export` into an instance:
//
//	cronova import <src-dir> -server http://host:8090 -token $TOKEN
//
// Idempotent upserts throughout: DAGs re-validate through the normal create
// path, pools/variables/connections overwrite same-named entries and never
// delete anything the bundle doesn't mention.
func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) != 1 {
		return fmt.Errorf("usage: cronova import <src-dir> (needs -server/-token or CRONOVA_SERVER/CRONOVA_TOKEN)")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx := context.Background()
	src := pos[0]

	// pools/variables/connections FIRST: DAG commands may reference them.
	if b, err := os.ReadFile(filepath.Join(src, "pools.json")); err == nil {
		var pools []struct {
			Name  string `json:"name"`
			Slots int    `json:"slots"`
		}
		if err := json.Unmarshal(b, &pools); err != nil {
			return fmt.Errorf("pools.json: %w", err)
		}
		for _, p := range pools {
			if _, err := c.Call(ctx, "POST", "/api/pools/{name}", client.Options{
				Path: map[string]string{"name": p.Name}, Query: map[string]string{"slots": strconv.Itoa(p.Slots)},
			}); err != nil {
				return fmt.Errorf("import pool %s: %w", p.Name, err)
			}
		}
		fmt.Printf("pools       → %d\n", len(pools))
	}
	if b, err := os.ReadFile(filepath.Join(src, "variables.json")); err == nil {
		var vars []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(b, &vars); err != nil {
			return fmt.Errorf("variables.json: %w", err)
		}
		for _, v := range vars {
			body, _ := json.Marshal(map[string]string{"value": v.Value})
			if _, err := c.Call(ctx, "POST", "/api/variables/{key}", client.Options{
				Path: map[string]string{"key": v.Key}, Body: body,
			}); err != nil {
				return fmt.Errorf("import variable %s: %w", v.Key, err)
			}
		}
		fmt.Printf("variables   → %d\n", len(vars))
	}
	if b, err := os.ReadFile(filepath.Join(src, "connections.json")); err == nil {
		var conns []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Host  string `json:"host"`
			Port  int    `json:"port"`
			Login string `json:"login"`
			Extra string `json:"extra"`
		}
		if err := json.Unmarshal(b, &conns); err != nil {
			return fmt.Errorf("connections.json: %w", err)
		}
		for _, cn := range conns {
			body, _ := json.Marshal(map[string]any{"type": cn.Type, "host": cn.Host, "port": cn.Port, "login": cn.Login, "extra": cn.Extra})
			if _, err := c.Call(ctx, "POST", "/api/connections/{id}", client.Options{
				Path: map[string]string{"id": cn.ID}, Body: body,
			}); err != nil {
				return fmt.Errorf("import connection %s: %w", cn.ID, err)
			}
		}
		if len(conns) > 0 {
			fmt.Printf("connections → %d (passwords NOT restored — re-enter them in the console)\n", len(conns))
		}
	}

	if b, err := os.ReadFile(filepath.Join(src, "alert-groups.json")); err == nil {
		var groups []struct {
			Name     string          `json:"name"`
			Channels json.RawMessage `json:"channels"`
		}
		if err := json.Unmarshal(b, &groups); err != nil {
			return fmt.Errorf("alert-groups.json: %w", err)
		}
		for _, gr := range groups {
			body, _ := json.Marshal(map[string]json.RawMessage{"channels": gr.Channels})
			if _, err := c.Call(ctx, "POST", "/api/alert-groups/{name}", client.Options{
				Path: map[string]string{"name": gr.Name}, Body: body,
			}); err != nil {
				return fmt.Errorf("import alert group %s: %w", gr.Name, err)
			}
		}
		if len(groups) > 0 {
			fmt.Printf("alert groups→ %d\n", len(groups))
		}
	}

	// DAGs last, via the same validated path `cronova apply` uses.
	dagDir := filepath.Join(src, "dags")
	if _, err := os.Stat(dagDir); err == nil {
		return cmdApply(append([]string{dagDir}, applyPassthroughFlags(g)...))
	}
	return nil
}

// applyPassthroughFlags reconstructs the global connection flags for the
// nested apply invocation so import's -server/-token carry through.
func applyPassthroughFlags(g globalOpts) []string {
	var out []string
	if g.server != "" {
		out = append(out, "-server", g.server)
	}
	if g.token != "" {
		out = append(out, "-token", g.token)
	}
	return out
}
