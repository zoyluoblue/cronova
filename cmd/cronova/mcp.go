package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoyluo/cronova/internal/client"
	"github.com/zoyluo/cronova/internal/mcp"
)

// cmdMCP runs an MCP server over stdio, exposing cronova's operations as tools
// for an AI client (Claude Code/Desktop, any MCP host). It talks to a running
// cronova server through the token-authenticated REST API — the AI's reach is
// exactly its token's role. STDOUT carries the protocol; logs go to STDERR.
//
//	cronova mcp                      # -> http://localhost:8090, token from CRONOVA_TOKEN
//	cronova mcp -server URL -token T
//	cronova mcp -read-only           # only expose read (GET) tools
func cmdMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	server := fs.String("server", "", "cronova server URL (default http://localhost:8090; env CRONOVA_SERVER)")
	token := fs.String("token", "", "API token (env CRONOVA_TOKEN)")
	readOnly := fs.Bool("read-only", false, "expose only read-only (GET) tools")
	_ = fs.Parse(args)

	srv := *server
	if srv == "" {
		srv = os.Getenv("CRONOVA_SERVER")
	}
	if srv == "" {
		srv = "http://localhost:8090"
	}
	tok := *token
	if tok == "" {
		tok = os.Getenv("CRONOVA_TOKEN")
	}

	cli := client.New(srv, tok)
	tools := mcp.BuildTools(cli, *readOnly)
	fmt.Fprintf(os.Stderr, "cronova mcp: serving %d tools over stdio (server=%s, read-only=%v)\n", len(tools), srv, *readOnly)
	if tok == "" {
		fmt.Fprintln(os.Stderr, "cronova mcp: warning — no token set; calls will fail if the server requires auth (mint one with `cronova tokens create`)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return mcp.NewServer("cronova", version, tools).Serve(ctx, os.Stdin, os.Stdout)
}
