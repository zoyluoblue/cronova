package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/zoyluo/cronova/internal/client"
)

// Remote mode + machine-readable output. An AI agent (or any script) drives
// cronova by pointing the CLI at a running server with a token and asking for
// JSON — the same token-authenticated, role-gated path a browser uses.

// globalOpts is the resolved remote/output configuration for a command.
type globalOpts struct {
	server string
	token  string
	output string // "table" (default) or "json"
}

// addGlobalFlags registers -server/-token/-o on fs and returns a resolver that
// applies CRONOVA_SERVER / CRONOVA_TOKEN / CRONOVA_OUTPUT as fallbacks.
func addGlobalFlags(fs *flag.FlagSet) func() globalOpts {
	server := fs.String("server", "", "remote cronova server URL, e.g. http://host:8090 (env CRONOVA_SERVER); empty = local DB")
	token := fs.String("token", "", "API token for remote mode (env CRONOVA_TOKEN)")
	output := fs.String("o", "", "output format: table (default) or json (env CRONOVA_OUTPUT)")
	return func() globalOpts {
		g := globalOpts{server: *server, token: *token, output: *output}
		if g.server == "" {
			g.server = os.Getenv("CRONOVA_SERVER")
		}
		if g.token == "" {
			g.token = os.Getenv("CRONOVA_TOKEN")
		}
		if g.output == "" {
			g.output = os.Getenv("CRONOVA_OUTPUT")
		}
		if g.output == "" {
			g.output = "table"
		}
		return g
	}
}

func (g globalOpts) remote() bool { return g.server != "" }
func (g globalOpts) asJSON() bool { return strings.EqualFold(g.output, "json") }

// client builds a remote API client, erroring if no server is configured.
func (g globalOpts) client() (*client.Client, error) {
	if g.server == "" {
		return nil, errors.New("this needs a server — pass -server/-token or set CRONOVA_SERVER/CRONOVA_TOKEN")
	}
	return client.New(g.server, g.token), nil
}

// printJSON pretty-prints a Go value to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printRawJSON pretty-prints already-encoded JSON (falling back to raw bytes).
func printRawJSON(b []byte) {
	var v any
	if json.Unmarshal(b, &v) == nil {
		_ = printJSON(v)
		return
	}
	os.Stdout.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
}

// emit prints an API result: pretty JSON for a JSON body, raw bytes otherwise.
func emit(res *client.Result) {
	if res == nil {
		return
	}
	if strings.Contains(res.ContentType, "json") {
		printRawJSON(res.Body)
		return
	}
	if len(res.Body) > 0 {
		os.Stdout.Write(res.Body)
		if res.Body[len(res.Body)-1] != '\n' {
			fmt.Println()
		}
	}
}

// callAndEmit runs a remote API call, prints the response, and exits non-zero on
// an API error (the error body is already printed). Transport/setup errors are
// returned for the caller to report.
func callAndEmit(g globalOpts, method, path string, opts client.Options) error {
	c, err := g.client()
	if err != nil {
		return err
	}
	res, err := c.Call(context.Background(), method, path, opts)
	emit(res)
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) {
			os.Exit(1)
		}
		return err
	}
	return nil
}

// cmdAPI is a raw passthrough to any API endpoint — the agent-friendly escape
// hatch that exposes the full REST surface without a per-endpoint subcommand.
//
//	cronova api GET  /api/dags
//	cronova api POST /api/dags/etl/trigger '{"params":{"day":"2026-01-01"}}'
func cmdAPI(args []string) error {
	fs := flag.NewFlagSet("api", flag.ExitOnError)
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, args)
	g := resolve()
	if len(pos) < 2 {
		return errors.New("usage: cronova api <METHOD> <path> [json-body]   (needs -server/-token or CRONOVA_SERVER/TOKEN)")
	}
	method, path := strings.ToUpper(pos[0]), pos[1]
	var body []byte
	if len(pos) >= 3 && pos[2] != "" {
		body = []byte(pos[2])
	}
	return callAndEmit(g, method, path, client.Options{Body: body})
}
