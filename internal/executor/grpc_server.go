package executor

import (
	"context"
	"time"

	pb "github.com/zoyluo/cronova/proto/cronova/executor/v1"
)

// GRPCServer adapts a Runner to the generated gRPC ExecutorServer. It is hosted
// by cmd/cronova-executor as a long-lived process.
type GRPCServer struct {
	pb.UnimplementedExecutorServer
	runner *Runner
}

func NewGRPCServer(r *Runner) *GRPCServer { return &GRPCServer{runner: r} }

func (s *GRPCServer) Launch(_ context.Context, req *pb.LaunchRequest) (*pb.LaunchResponse, error) {
	ref, err := s.runner.Launch(Spec{
		TaskRunID: req.GetTaskRunId(),
		Type:      req.GetType(),
		Command:   req.GetCommand(),
		Env:       req.GetEnv(),
		Timeout:   time.Duration(req.GetTimeoutSeconds()) * time.Second,
		LogPath:   req.GetLogPath(),
		Dir:       req.GetDir(),
		Redact:    req.GetRedact(),
	})
	if err != nil {
		return nil, err
	}
	return &pb.LaunchResponse{Ref: ref}, nil
}

func (s *GRPCServer) Probe(_ context.Context, req *pb.ProbeRequest) (*pb.ProbeResponse, error) {
	st := s.runner.Probe(req.GetRef())
	return &pb.ProbeResponse{Phase: toPBPhase(st.Phase), ExitCode: int32(st.ExitCode)}, nil
}

func (s *GRPCServer) Cancel(_ context.Context, req *pb.CancelRequest) (*pb.CancelResponse, error) {
	if err := s.runner.Cancel(req.GetRef()); err != nil {
		return &pb.CancelResponse{Ok: false}, err
	}
	return &pb.CancelResponse{Ok: true}, nil
}

func toPBPhase(p Phase) pb.Phase {
	switch p {
	case PhaseRunning:
		return pb.Phase_PHASE_RUNNING
	case PhaseExited:
		return pb.Phase_PHASE_EXITED
	default:
		return pb.Phase_PHASE_UNKNOWN
	}
}
