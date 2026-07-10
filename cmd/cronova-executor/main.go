// Command cronova-executor is the long-lived task executor. The scheduler
// dispatches tasks to it over a local gRPC socket; because it is a separate
// process, a scheduler restart does not kill running tasks — the scheduler
// re-attaches by probing them (see docs/ARCHITECTURE.md §8–§9).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/zoyluo/cronova/internal/executor"
	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
)

func main() {
	sock := flag.String("sock", defaultSocketPath(), "unix socket path to listen on (parent directory must be private)")
	flag.Parse()
	if err := run(*sock); err != nil {
		log.Fatalf("cronova-executor: %v", err)
	}
}

func run(sock string) error {
	lis, cleanup, err := listenExecutorSocket(sock)
	if err != nil {
		return err
	}
	defer lis.Close()
	defer cleanup()

	runner := executor.NewRunner()
	srv := grpc.NewServer()
	pb.RegisterExecutorServer(srv, executor.NewGRPCServer(runner))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		log.Println("cronova-executor shutting down")
		srv.GracefulStop()
	}()

	log.Printf("cronova-executor listening on unix://%s", sock)
	err = srv.Serve(lis)
	// After GracefulStop, kill any still-running task process groups so they are
	// not left as orphans.
	runner.Shutdown()
	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

func defaultSocketPath() string {
	// Keep this path short: Darwin limits Unix socket paths to roughly 104 bytes,
	// while os.TempDir() can itself be a long /var/folders/... path.
	return filepath.Join("/tmp", fmt.Sprintf("cronova-%d", os.Getuid()), "executor.sock")
}

func listenExecutorSocket(sock string) (net.Listener, func(), error) {
	if sock == "" || !filepath.IsAbs(sock) {
		return nil, nil, fmt.Errorf("socket path must be absolute")
	}
	dir := filepath.Dir(sock)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create socket directory: %w", err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("stat socket directory: %w", err)
	}
	if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
		return nil, nil, fmt.Errorf("socket directory %s must be private (mode 0700)", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, nil, fmt.Errorf("socket directory %s must be owned by uid %d", dir, os.Geteuid())
	}
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		return nil, nil, fmt.Errorf("remove stale socket: %w", err)
	}
	lis, err := net.Listen("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.Remove(sock) }
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = lis.Close()
		cleanup()
		return nil, nil, fmt.Errorf("secure socket: %w", err)
	}
	return lis, cleanup, nil
}
