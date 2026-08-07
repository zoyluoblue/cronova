package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zoyluo/cronova/internal/client"
	"github.com/zoyluo/cronova/internal/scheduler/parser"
)

// cmdApply pushes a directory (or single file) of DAG YAML to a RUNNING server
// — the GitOps deploy step:
//
//	cronova apply dags/ -server http://host:8090 -token $TOKEN
//	cronova apply dags/ -dry-run        # plan only: create/update/unchanged
//
// It validates every file locally first (nothing is pushed if any file is
// broken), diffs against the server, and then POSTs only the files that
// actually changed. It deliberately requires the API rather than writing the
// DB: only the running scheduler can refresh its in-memory cache, and the
// server write-path re-validates with the same parser. Deletions are never
// synced — archiving a DAG stays an explicit action.
func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show the plan (create/update/unchanged) without pushing")
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) != 1 {
		return fmt.Errorf("usage: cronova apply <dir-or-file> [-dry-run] (needs -server/-token or CRONOVA_SERVER/CRONOVA_TOKEN)")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx := context.Background()

	files, err := collectYAMLFiles(pos[0])
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no *.yaml/*.yml files under %s", pos[0])
	}

	// Validate everything locally BEFORE touching the server: one broken file
	// aborts the whole apply (no half-deployed directory).
	type item struct {
		file, dagID string
		raw         []byte
	}
	var items []item
	seen := map[string]string{}
	var failed bool
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		d, err := parser.Parse(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "INVALID  %s: %v\n", f, err)
			failed = true
			continue
		}
		if prev, dup := seen[d.DagID]; dup {
			fmt.Fprintf(os.Stderr, "INVALID  %s: dag_id %q already defined in %s\n", f, d.DagID, prev)
			failed = true
			continue
		}
		seen[d.DagID] = f
		items = append(items, item{file: f, dagID: d.DagID, raw: raw})
	}
	if failed {
		return fmt.Errorf("validation failed — nothing was applied")
	}

	// Diff against the server: unchanged files are skipped entirely.
	var creates, updates, unchanged []item
	for _, it := range items {
		var remote struct {
			DefinitionYAML string `json:"definition_yaml"`
		}
		res, err := c.CallJSON(ctx, "GET", "/api/dags/{id}", client.Options{Path: map[string]string{"id": it.dagID}}, &remote)
		switch {
		case err == nil:
			if remote.DefinitionYAML == string(it.raw) {
				unchanged = append(unchanged, it)
			} else {
				updates = append(updates, it)
			}
		case res != nil && res.Status == 404:
			creates = append(creates, it)
		default:
			return fmt.Errorf("diff %s: %w", it.dagID, err)
		}
	}

	report := func(tag string, list []item) {
		sort.Slice(list, func(i, j int) bool { return list[i].dagID < list[j].dagID })
		for _, it := range list {
			fmt.Printf("%-9s %s (%s)\n", tag, it.dagID, filepath.Base(it.file))
		}
	}
	report("create", creates)
	report("update", updates)
	report("unchanged", unchanged)
	fmt.Printf("plan: %d to create, %d to update, %d unchanged\n", len(creates), len(updates), len(unchanged))
	if *dryRun {
		return nil
	}
	if len(creates)+len(updates) == 0 {
		return nil
	}

	var applyErrs int
	for _, it := range append(creates, updates...) {
		if _, err := c.Call(ctx, "POST", "/api/dags", client.Options{Body: it.raw, ContentType: "text/yaml"}); err != nil {
			fmt.Fprintf(os.Stderr, "FAILED   %s: %v\n", it.dagID, err)
			applyErrs++
			continue
		}
		fmt.Printf("applied   %s\n", it.dagID)
	}
	if applyErrs > 0 {
		return fmt.Errorf("%d of %d applies failed", applyErrs, len(creates)+len(updates))
	}
	return nil
}

// collectYAMLFiles returns path itself (if a YAML file) or the *.yaml/*.yml
// files directly inside it (no recursion — mirrors the server's DagDir scan).
func collectYAMLFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml") {
			out = append(out, filepath.Join(path, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}
