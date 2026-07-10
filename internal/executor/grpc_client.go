package executor

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// GRPCClient is an Executor backed by a remote cronova-executor process. The
// scheduler uses it so that a scheduler restart leaves running tasks untouched
// (they belong to the executor process) and can be re-attached via Probe.
type GRPCClient struct {
	conn   *grpc.ClientConn
	cli    pb.ExecutorClient
	health healthpb.HealthClient
}

var _ Executor = (*GRPCClient)(nil)

// Dial connects to an executor over an absolute Unix socket. The executor has
// no application-layer authentication, so TCP targets are intentionally
// rejected; filesystem ownership and socket mode form the trust boundary.
func Dial(target string) (*GRPCClient, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "unix" || !filepath.IsAbs(u.Path) || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("executor target must be an absolute unix:///path socket, got %q", target)
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial executor %q: %w", target, err)
	}
	c := &GRPCClient{conn: conn, cli: pb.NewExecutorClient(conn), health: healthpb.NewHealthClient(conn)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Health(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("executor %q is not ready: %w", target, err)
	}
	return c, nil
}

func (c *GRPCClient) Close() error { return c.conn.Close() }

const executorRPCTimeout = 5 * time.Second

func rpcContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, executorRPCTimeout)
}

// Health verifies that the remote executor process is reachable and serving.
func (c *GRPCClient) Health(ctx context.Context) error {
	ctx, cancel := rpcContext(ctx)
	defer cancel()
	resp, err := c.health.Check(ctx, &healthpb.HealthCheckRequest{}, grpc.WaitForReady(true))
	if err != nil {
		return err
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("health status is %s", resp.GetStatus())
	}
	return nil
}

func (c *GRPCClient) Launch(ctx context.Context, spec Spec) (string, error) {
	ctx, cancel := rpcContext(ctx)
	defer cancel()
	resp, err := c.cli.Launch(ctx, &pb.LaunchRequest{
		TaskRunId:      spec.TaskRunID,
		Type:           spec.Type,
		Command:        spec.Command,
		Env:            spec.Env,
		TimeoutSeconds: int64(spec.Timeout / time.Second),
		LogPath:        spec.LogPath,
		Dir:            spec.Dir,
		Redact:         spec.Redact,
	})
	if err != nil {
		return "", err
	}
	return resp.GetRef(), nil
}

func (c *GRPCClient) Probe(ctx context.Context, ref string) (Status, error) {
	ctx, cancel := rpcContext(ctx)
	defer cancel()
	resp, err := c.cli.Probe(ctx, &pb.ProbeRequest{Ref: ref})
	if err != nil {
		return Status{}, err
	}
	return Status{Phase: fromPBPhase(resp.GetPhase()), ExitCode: int(resp.GetExitCode())}, nil
}

func (c *GRPCClient) Cancel(ctx context.Context, ref string) error {
	ctx, cancel := rpcContext(ctx)
	defer cancel()
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
