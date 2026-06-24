package executor

import (
	"context"
	"fmt"
	"time"

	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCClient is an Executor backed by a remote cronova-executor process. The
// scheduler uses it so that a scheduler restart leaves running tasks untouched
// (they belong to the executor process) and can be re-attached via Probe.
type GRPCClient struct {
	conn *grpc.ClientConn
	cli  pb.ExecutorClient
}

var _ Executor = (*GRPCClient)(nil)

// Dial connects to an executor server. target is a gRPC dial target, e.g.
// "unix:///tmp/cronova-executor.sock" or "localhost:9091". The connection is
// lazy; the first RPC establishes it.
func Dial(target string) (*GRPCClient, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial executor %q: %w", target, err)
	}
	return &GRPCClient{conn: conn, cli: pb.NewExecutorClient(conn)}, nil
}

func (c *GRPCClient) Close() error { return c.conn.Close() }

func (c *GRPCClient) Launch(ctx context.Context, spec Spec) (string, error) {
	resp, err := c.cli.Launch(ctx, &pb.LaunchRequest{
		TaskRunId:      spec.TaskRunID,
		Type:           spec.Type,
		Command:        spec.Command,
		Env:            spec.Env,
		TimeoutSeconds: int64(spec.Timeout / time.Second),
		LogPath:        spec.LogPath,
	})
	if err != nil {
		return "", err
	}
	return resp.GetRef(), nil
}

func (c *GRPCClient) Probe(ctx context.Context, ref string) (Status, error) {
	resp, err := c.cli.Probe(ctx, &pb.ProbeRequest{Ref: ref})
	if err != nil {
		return Status{}, err
	}
	return Status{Phase: fromPBPhase(resp.GetPhase()), ExitCode: int(resp.GetExitCode())}, nil
}

func (c *GRPCClient) Cancel(ctx context.Context, ref string) error {
	_, err := c.cli.Cancel(ctx, &pb.CancelRequest{Ref: ref})
	return err
}

func fromPBPhase(p pb.Phase) Phase {
	switch p {
	case pb.Phase_PHASE_RUNNING:
		return PhaseRunning
	case pb.Phase_PHASE_EXITED:
		return PhaseExited
	default:
		return PhaseUnknown
	}
}
