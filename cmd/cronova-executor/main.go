// Command cronova-executor is the long-lived task executor. The scheduler
// dispatches tasks to it over a local gRPC socket; because it is a separate
// process, a scheduler restart does not kill running tasks — the scheduler
// re-attaches by probing them (see docs/ARCHITECTURE.md §8–§9).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/zoyluo/cronova/internal/executor"
	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
)

func main() {
	sock := flag.String("sock", "/tmp/cronova-executor.sock", "unix socket path to listen on")
	flag.Parse()
	if err := run(*sock); err != nil {
		log.Fatalf("cronova-executor: %v", err)
	}
}

func run(sock string) error {
	// Remove a stale socket from a previous run.
	if err := os.Remove(sock); err != nil && !os.IsNotExist(err) {
		return err
	}
	lis, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer os.Remove(sock)

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
