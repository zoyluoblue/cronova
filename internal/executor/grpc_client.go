package executor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

// Dial connects to an executor: an absolute unix:///path socket (same host —
// filesystem ownership is the trust boundary), or tcp://host:port under
// MANDATORY mutual TLS (both sides verify certificates; there is no plaintext
// TCP mode). The mTLS material comes from CRONOVA_EXEC_TLS_CERT / _KEY / _CA
// (PEM file paths) so the target string stays a plain address.
//
// A remote executor runs tasks on ITS host: task logs are written there, and
// project attach requires a shared filesystem (the runner reports that error
// explicitly). Suits offloading compute; log streaming lands in a later phase.
func Dial(target string) (*GRPCClient, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid executor target %q: %w", target, err)
	}
	var creds grpc.DialOption
	switch {
	case u.Scheme == "unix" && filepath.IsAbs(u.Path) && u.Host == "" && u.RawQuery == "" && u.Fragment == "":
		creds = grpc.WithTransportCredentials(insecure.NewCredentials())
	case u.Scheme == "tcp" && u.Host != "":
		tc, err := clientMTLS()
		if err != nil {
			return nil, fmt.Errorf("executor tcp target requires mutual TLS: %w", err)
		}
		creds = grpc.WithTransportCredentials(tc)
		target = u.Host // grpc dials host:port with the default resolver
	default:
		return nil, fmt.Errorf("executor target must be unix:///abs/path or tcp://host:port, got %q", target)
	}
	conn, err := grpc.NewClient(target, creds)
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

// clientMTLS loads the scheduler-side certificate pair and the CA that signed
// the executor's certificate from CRONOVA_EXEC_TLS_CERT / _KEY / _CA. All
// three are required — a TCP executor link is always mutually authenticated.
func clientMTLS() (credentials.TransportCredentials, error) {
	certFile, keyFile, caFile := os.Getenv("CRONOVA_EXEC_TLS_CERT"), os.Getenv("CRONOVA_EXEC_TLS_KEY"), os.Getenv("CRONOVA_EXEC_TLS_CA")
	if certFile == "" || keyFile == "" || caFile == "" {
		return nil, fmt.Errorf("set CRONOVA_EXEC_TLS_CERT, CRONOVA_EXEC_TLS_KEY and CRONOVA_EXEC_TLS_CA (PEM paths)")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no certificates in %s", caFile)
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}), nil
}
