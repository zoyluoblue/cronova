package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zoyluo/cronova/internal/api"
	"github.com/zoyluo/cronova/internal/auth"
	"github.com/zoyluo/cronova/internal/certs"
	"github.com/zoyluo/cronova/internal/client"
	"github.com/zoyluo/cronova/internal/scheduler"
	"github.com/zoyluo/cronova/internal/worker"
	"github.com/zoyluo/cronova/internal/workerhub"
	workerv1 "github.com/zoyluo/cronova/proto/cronova/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// startWorkerHub wires the dial-in worker hub into a running serve: embedded
// CA next to the key file, an mTLS gRPC listener for worker Sessions, the
// scheduler's remote routing, and the join/manage API endpoints. Returns a
// stop function.
func startWorkerHub(ctx context.Context, cfg Config, st serverStore, sch *scheduler.Scheduler, apiSrv *api.Server, logger *slog.Logger) (stop func(), err error) {
	caDir := filepath.Dir(cfg.KeyFile)
	if cfg.KeyFile == "" || cfg.KeyFile == "none" {
		caDir = filepath.Dir(cfg.DB)
	}
	ca, created, err := certs.LoadOrCreateCA(caDir)
	if err != nil {
		return nil, fmt.Errorf("worker hub CA: %w", err)
	}
	if created {
		log.Printf("cronova: generated worker-hub CA in %s — back up cronova-ca.key with the rest of your key material", caDir)
	}

	advertise := cfg.WorkerAdvertise
	if advertise == "" {
		// Best effort: same port as the listener on an unspecified host — the
		// join response tells workers where to dial, so an explicit
		// worker_advertise is strongly recommended for multi-host setups.
		if _, port, perr := net.SplitHostPort(cfg.WorkerListen); perr == nil {
			advertise = net.JoinHostPort(hostOf(cfg.HTTP), port)
		}
	}

	hosts := []string{"localhost", "127.0.0.1"}
	if h := hostOf(advertise); h != "" {
		hosts = append(hosts, h)
	}
	certPEM, keyPEM, err := ca.ServerCert(hosts, 10*365*24*time.Hour)
	if err != nil {
		return nil, err
	}
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := certs.CAPool(ca)
	// Plain TCP listener: grpc.Creds terminates mTLS itself, which is also
	// what exposes the verified client certificate (worker identity) to the
	// hub's Session handler.
	ln, err := net.Listen("tcp", cfg.WorkerListen)
	if err != nil {
		return nil, fmt.Errorf("worker hub listen %s: %w", cfg.WorkerListen, err)
	}

	hub := workerhub.New(st, workerhub.Options{Logger: logger})
	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS12,
	})))
	workerv1.RegisterWorkerHubServer(grpcSrv, hub)
	go func() {
		if serr := grpcSrv.Serve(ln); serr != nil && ctx.Err() == nil {
			log.Printf("cronova: worker hub server error: %v", serr)
		}
	}()
	go hub.Run(ctx)

	sch.SetWorkerHub(hub)
	if apiSrv != nil {
		apiSrv.SetWorkerHub(ca, advertise, hub)
	}
	// Pre-seeded join tokens (orchestrated stacks): one-time, 24h TTL, hashed.
	// A token already seeded by a previous boot upserts as a fresh unused row —
	// safe because joins burn tokens atomically.
	for i, tok := range cfg.WorkerJoinTokens {
		if len(tok) < 16 {
			log.Printf("cronova: WARNING worker_join_tokens[%d] is shorter than 16 chars — refusing to seed a guessable token", i)
			continue
		}
		if err := st.CreateJoinToken(context.Background(), auth.HashAPIToken(tok), "config", time.Now().UTC().Add(24*time.Hour)); err != nil {
			log.Printf("cronova: seed join token[%d]: %v (already seeded and consumed?)", i, err)
		}
	}
	log.Printf("cronova: worker hub listening on %s (advertising %q to joining workers)", cfg.WorkerListen, advertise)
	return grpcSrv.GracefulStop, nil
}

func hostOf(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil || h == "" {
		return ""
	}
	return h
}

// cmdWorker runs (and optionally first joins) a dial-in worker:
//
//	cronova worker -server http://sched:8090 -join-token cwj_... [-name w1] [-labels group=gpu,zone=eu]
//	cronova worker                                    # already joined: just run
func cmdWorker(args []string) error {
	fs := flag.NewFlagSet("worker", flag.ExitOnError)
	stateDir := fs.String("state-dir", defaultWorkerStateDir(), "directory for worker identity, attempt state, and log spool")
	server := fs.String("server", envOr("CRONOVA_SERVER", ""), "scheduler base URL (join only)")
	joinToken := fs.String("join-token", os.Getenv("CRONOVA_JOIN_TOKEN"), "one-time join token (mint with: cronova workers token)")
	name := fs.String("name", hostnameDefault(), "worker display name")
	labelsFlag := fs.String("labels", "group=default", "comma-separated key=value routing labels; the group label is the worker group")
	hubOverride := fs.String("hub", "", "override the hub address advertised by the server (host:port)")
	_ = fs.Parse(args)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	switch {
	case worker.Joined(*stateDir):
		// Restart-idempotent: an existing identity wins, so a container that
		// keeps its -join-token env does not mint a new worker every restart.
		if *joinToken != "" {
			log.Printf("cronova worker: already joined (state in %s) — ignoring -join-token", *stateDir)
		}
	case *joinToken != "":
		if *server == "" {
			return fmt.Errorf("-server is required to join (the scheduler's console URL)")
		}
		labels, err := parseLabels(*labelsFlag)
		if err != nil {
			return err
		}
		id, err := worker.Join(*stateDir, *server, *joinToken, *name, labels, *hubOverride)
		if err != nil {
			return err
		}
		fmt.Printf("joined as %s (state in %s)\n", id, *stateDir)
	default:
		return fmt.Errorf("no worker identity in %s — join first: cronova worker -server <url> -join-token <token>", *stateDir)
	}

	a, err := worker.New(*stateDir, version, logger)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err = a.Run(ctx)
	a.Shutdown() // leaves processes running for re-adoption on the next start
	return err
}

func parseLabels(s string) (map[string]string, error) {
	labels := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid label %q (want key=value)", kv)
		}
		labels[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return labels, nil
}

func defaultWorkerStateDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cronova", "worker")
	}
	return ".cronova-worker"
}

func hostnameDefault() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "worker"
}

// cmdWorkers manages the fleet through the REST API:
//
//	cronova workers list                     -server ... -token ...
//	cronova workers token [-ttl 24h]         # mint a one-time join token
//	cronova workers drain <worker_id> [-off]
//	cronova workers remove <worker_id>
func cmdWorkers(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cronova workers <list|token|drain|remove> [args]")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("workers "+sub, flag.ExitOnError)
	ttl := fs.String("ttl", "", "join token validity (token subcommand; default 24h)")
	off := fs.Bool("off", false, "undrain instead of drain (drain subcommand)")
	resolve := addGlobalFlags(fs)
	pos := parsePositionals(fs, rest)
	g := resolve()
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch sub {
	case "list":
		var raw []map[string]any
		if _, err := c.CallJSON(ctx, "GET", "/api/workers", client.Options{}, &raw); err != nil {
			return err
		}
		if g.asJSON() {
			return printJSON(raw)
		}
		if len(raw) == 0 {
			fmt.Println("no workers joined. mint a token with `cronova workers token`, then run `cronova worker -server <url> -join-token <t>` on the worker host.")
			return nil
		}
		fmt.Printf("%-14s %-16s %-10s %-8s %-6s %s\n", "WORKER", "NAME", "GROUP", "STATE", "TASKS", "LAST HEARTBEAT")
		for _, w := range raw {
			group := "default"
			if labels, ok := w["labels"].(map[string]any); ok {
				if gv, ok := labels["group"].(string); ok && gv != "" {
					group = gv
				}
			}
			state, _ := w["state"].(string)
			if d, _ := w["draining"].(bool); d {
				state += "+drain"
			}
			hb, _ := w["last_heartbeat"].(string)
			fmt.Printf("%-14v %-16v %-10s %-8s %-6v %s\n", w["worker_id"], w["name"], group, state, w["active_tasks"], hb)
		}
		return nil
	case "token":
		var body []byte
		if *ttl != "" {
			body, _ = json.Marshal(map[string]string{"ttl": *ttl})
		}
		var out map[string]string
		if _, err := c.CallJSON(ctx, "POST", "/api/worker-tokens", client.Options{Body: body}, &out); err != nil {
			return err
		}
		if g.asJSON() {
			return printJSON(out)
		}
		fmt.Printf("join token (one-time, expires %s):\n%s\n", out["expires_at"], out["token"])
		fmt.Println("on the worker host: cronova worker -server <console-url> -join-token <token>")
		return nil
	case "drain":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cronova workers drain <worker_id> [-off]")
		}
		body, _ := json.Marshal(map[string]bool{"draining": !*off})
		if _, err := c.Call(ctx, "POST", "/api/workers/{id}/drain", client.Options{
			Path: map[string]string{"id": pos[0]}, Body: body,
		}); err != nil {
			return err
		}
		fmt.Printf("worker %s draining=%v\n", pos[0], !*off)
		return nil
	case "remove":
		if len(pos) != 1 {
			return fmt.Errorf("usage: cronova workers remove <worker_id>")
		}
		if _, err := c.Call(ctx, "DELETE", "/api/workers/{id}", client.Options{
			Path: map[string]string{"id": pos[0]},
		}); err != nil {
			return err
		}
		fmt.Printf("worker %s removed\n", pos[0])
		return nil
	}
	return fmt.Errorf("unknown workers subcommand %q (list, token, drain, remove)", sub)
}
