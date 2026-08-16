// Package worker is the dial-in remote task runner: it joins a cronova
// scheduler with a one-time token (receiving an mTLS client certificate for a
// locally generated key), then holds one long-lived Session stream to the
// scheduler's worker hub — receiving task assignments and streaming back task
// events and log bytes. Processes are run by the same executor.Runner the
// standalone executor uses, so a worker restart re-adopts its tasks instead
// of killing them.
package worker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	workerv1 "github.com/zoyluo/cronova/proto/cronova/worker/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// identity is the persisted result of a successful join (state-dir files hold
// the key material; this holds the metadata).
type identity struct {
	WorkerID string            `json:"worker_id"`
	Name     string            `json:"name"`
	Labels   map[string]string `json:"labels,omitempty"`
	HubAddr  string            `json:"hub_addr"`
}

// Agent is a running worker.
type Agent struct {
	stateDir string
	id       identity
	runner   *executor.Runner
	log      *slog.Logger
	version  string

	mu   sync.Mutex
	refs map[string]*refTrack
	out  chan *workerv1.WorkerMessage // current session's outbound queue
}

// refTrack is the worker-side view of one assignment.
type refTrack struct {
	spool    string // local log file the Runner writes
	outFile  string // local $CRONOVA_OUTPUT destination
	ackOff   int64  // bytes the hub has confirmed durable
	rewind   bool   // hub demanded a restart from ackOff (gap on its side)
	exited   bool
	exitCode int
	ackCh    chan int64 // pump wakeups on LogAck
}

// New loads a joined worker from stateDir. Run Join first on a fresh host.
func New(stateDir, version string, logger *slog.Logger) (*Agent, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, "worker.json"))
	if err != nil {
		return nil, fmt.Errorf("worker identity not found — run `cronova worker -join-token ...` first: %w", err)
	}
	var id identity
	if err := json.Unmarshal(b, &id); err != nil {
		return nil, err
	}
	runner, err := executor.NewRunnerWithState(filepath.Join(stateDir, "attempts"))
	if err != nil {
		return nil, err
	}
	if adopted, finished := runner.RecoverState(); adopted+finished > 0 {
		logger.Info("re-adopted attempts from previous worker process", "running", adopted, "finished", finished)
	}
	a := &Agent{
		stateDir: stateDir,
		id:       id,
		runner:   runner,
		log:      logger.With("comp", "worker", "worker", id.WorkerID),
		version:  version,
		refs:     map[string]*refTrack{},
	}
	// Attempts that survived a worker restart need their bookkeeping back so
	// log pumps and exit watchers resume (ack offsets restart at 0 — the hub
	// dedups by offset).
	for _, ref := range runner.ActiveRefs() {
		a.refs[ref] = a.newTrack(ref)
		go a.watchExit(ref)
	}
	return a, nil
}

func (a *Agent) newTrack(ref string) *refTrack {
	base := filepath.Join(a.stateDir, "spool", sanitizeRef(ref))
	return &refTrack{
		spool:   base + ".log",
		outFile: base + ".out",
		ackCh:   make(chan int64, 4),
	}
}

// sanitizeRef maps an attempt ref (run/task/try) to a filesystem-safe name.
func sanitizeRef(ref string) string {
	return strings.NewReplacer("/", "_", string(filepath.Separator), "_").Replace(ref)
}

// Run connects (and reconnects, with backoff) until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	creds, err := a.tlsCreds()
	if err != nil {
		return err
	}
	backoff := time.Second
	for {
		if err := a.session(ctx, creds); err != nil && ctx.Err() == nil {
			a.log.Warn("session ended, reconnecting", "err", err, "backoff", backoff)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) tlsCreds() (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(a.stateDir, "worker.crt"), filepath.Join(a.stateDir, "worker.key"))
	if err != nil {
		return nil, fmt.Errorf("load worker certificate: %w", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(a.stateDir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("worker: malformed ca.crt")
	}
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   "cronova-hub", // fixed SAN issued by the pinned private CA
		MinVersion:   tls.VersionTLS12,
	}), nil
}

// session runs one connection lifetime.
func (a *Agent) session(ctx context.Context, creds credentials.TransportCredentials) error {
	conn, err := grpc.NewClient(a.id.HubAddr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return err
	}
	defer conn.Close()
	stream, err := workerv1.NewWorkerHubClient(conn).Session(ctx)
	if err != nil {
		return err
	}

	out := make(chan *workerv1.WorkerMessage, 256)
	a.mu.Lock()
	a.out = out
	a.mu.Unlock()
	active := a.runner.ActiveRefs()

	hello := &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Hello{Hello: &workerv1.Hello{
		WorkerId:   a.id.WorkerID,
		Name:       a.id.Name,
		Labels:     a.id.Labels,
		Version:    a.version,
		ActiveRefs: active,
	}}}
	if err := stream.Send(hello); err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	ack := first.GetHelloAck()
	if ack == nil {
		return errors.New("worker: expected HelloAck")
	}
	hb := time.Duration(ack.HeartbeatSeconds) * time.Second
	if hb <= 0 {
		hb = 5 * time.Second
	}
	a.log.Info("connected to hub", "addr", a.id.HubAddr, "heartbeat", hb)

	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Writer: sole owner of stream.Send after Hello.
	writeErr := make(chan error, 1)
	go func() {
		hbT := time.NewTicker(hb)
		defer hbT.Stop()
		for {
			select {
			case <-sessCtx.Done():
				writeErr <- nil
				return
			case <-hbT.C:
				n := len(a.runner.ActiveRefs())
				msg := &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_Heartbeat{Heartbeat: &workerv1.Heartbeat{ActiveTasks: int32(n)}}}
				if err := stream.Send(msg); err != nil {
					writeErr <- err
					return
				}
			case m := <-out:
				if err := stream.Send(m); err != nil {
					writeErr <- err
					return
				}
			}
		}
	}()

	// Restart a log pump for every tracked ref (fresh session, resend from ack).
	a.mu.Lock()
	for ref := range a.refs {
		go a.pumpLogs(sessCtx, ref, out)
	}
	a.mu.Unlock()

	// Reader loop.
	for {
		in, err := stream.Recv()
		if err != nil {
			cancel()
			<-writeErr
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch m := in.Msg.(type) {
		case *workerv1.ServerMessage_Assign:
			a.handleAssign(sessCtx, m.Assign, out)
		case *workerv1.ServerMessage_Cancel:
			_ = a.runner.Cancel(m.Cancel.Ref)
		case *workerv1.ServerMessage_Probe:
			a.answerProbe(m.Probe.Ref, out)
		case *workerv1.ServerMessage_LogAck:
			a.handleAck(m.LogAck)
		}
	}
}

func (a *Agent) handleAssign(ctx context.Context, as *workerv1.Assign, out chan *workerv1.WorkerMessage) {
	a.mu.Lock()
	tr, exists := a.refs[as.Ref]
	if !exists {
		tr = a.newTrack(as.Ref)
		a.refs[as.Ref] = tr
	}
	a.mu.Unlock()
	if exists {
		return // idempotent: already tracking (Runner.Launch is also idempotent)
	}
	_ = os.MkdirAll(filepath.Dir(tr.spool), 0o700)
	env := make(map[string]string, len(as.Env)+1)
	for k, v := range as.Env {
		env[k] = v
	}
	env["CRONOVA_OUTPUT"] = tr.outFile // the scheduler-local path is meaningless here
	spec := executor.Spec{
		TaskRunID: as.Ref,
		Type:      as.Type,
		Command:   as.Command,
		Env:       env,
		Timeout:   time.Duration(as.TimeoutSeconds) * time.Second,
		LogPath:   tr.spool,
		Redact:    as.Redact,
	}
	if _, err := a.runner.Launch(spec); err != nil {
		a.log.Error("launch failed", "ref", as.Ref, "err", err)
		a.sendReliable(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_TaskEvent{TaskEvent: &workerv1.TaskEvent{
			Ref: as.Ref, Phase: workerv1.Phase_PHASE_EXITED, ExitCode: 127, LaunchError: err.Error(),
		}}})
		return
	}
	a.log.Info("task launched", "ref", as.Ref)
	go a.watchExit(as.Ref)
	go a.pumpLogs(ctx, as.Ref, out)
}

// watchExit polls the Runner until the attempt exits, then reports the
// terminal TaskEvent (with the task's emitted output, if any). It survives
// session drops: the event goes to whatever the CURRENT session queue is.
func (a *Agent) watchExit(ref string) {
	for {
		st := a.runner.Probe(ref)
		switch st.Phase {
		case executor.PhaseRunning:
			time.Sleep(500 * time.Millisecond)
			continue
		case executor.PhaseExited, executor.PhaseUnknown:
			a.mu.Lock()
			tr := a.refs[ref]
			if tr != nil {
				tr.exited = true
				tr.exitCode = st.ExitCode
			}
			a.mu.Unlock()
			ev := &workerv1.TaskEvent{Ref: ref, Phase: workerv1.Phase_PHASE_EXITED, ExitCode: int32(st.ExitCode)}
			if st.Phase == executor.PhaseUnknown {
				ev.Phase = workerv1.Phase_PHASE_UNKNOWN
			}
			if tr != nil {
				if b, err := os.ReadFile(tr.outFile); err == nil && len(b) <= 64<<10 {
					ev.Output = b
				}
			}
			// The terminal event is what unblocks the scheduler — it must not
			// be dropped by a jammed log queue (the old non-blocking send left
			// exited tasks stuck "running" on the hub until a reconnect).
			a.sendReliable(&workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_TaskEvent{TaskEvent: ev}})
			a.log.Info("task exited", "ref", ref, "exit", st.ExitCode)
			return
		}
	}
}

// answerProbe re-reports a ref's state on the hub's request (recovery after a
// reconnect where the terminal event may have been lost).
func (a *Agent) answerProbe(ref string, out chan *workerv1.WorkerMessage) {
	st := a.runner.Probe(ref)
	ev := &workerv1.TaskEvent{Ref: ref}
	switch st.Phase {
	case executor.PhaseRunning:
		ev.Phase = workerv1.Phase_PHASE_RUNNING
	case executor.PhaseExited:
		ev.Phase = workerv1.Phase_PHASE_EXITED
		ev.ExitCode = int32(st.ExitCode)
		a.mu.Lock()
		tr := a.refs[ref]
		a.mu.Unlock()
		if tr != nil {
			if b, err := os.ReadFile(tr.outFile); err == nil && len(b) <= 64<<10 {
				ev.Output = b
			}
		}
	default:
		ev.Phase = workerv1.Phase_PHASE_UNKNOWN
	}
	send(out, &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_TaskEvent{TaskEvent: ev}})
}

func (a *Agent) handleAck(ack *workerv1.LogAck) {
	a.mu.Lock()
	tr := a.refs[ack.Ref]
	if tr != nil && ack.Rewind {
		// The hub found a gap (e.g. its copy was pruned): restart streaming
		// from ITS high-water mark. This is the only path that moves the
		// cursor backward — ordinary acks are monotonic.
		tr.ackOff = ack.Offset
		tr.rewind = true
	} else if tr != nil && ack.Offset > tr.ackOff {
		tr.ackOff = ack.Offset
		select {
		case tr.ackCh <- ack.Offset:
		default:
		}
	}
	// A fully-acked, exited ref can be garbage collected.
	if tr != nil && tr.exited {
		if fi, err := os.Stat(tr.spool); err == nil && tr.ackOff >= fi.Size() {
			_ = os.Remove(tr.spool)
			_ = os.Remove(tr.outFile)
			delete(a.refs, ack.Ref)
		}
	}
	a.mu.Unlock()
}

// pumpLogs streams the spool file to the hub from the last acknowledged
// offset. It ends with the session (ctx) or when the attempt has exited and
// every byte is sent.
func (a *Agent) pumpLogs(ctx context.Context, ref string, out chan *workerv1.WorkerMessage) {
	const chunk = 32 << 10
	a.mu.Lock()
	tr := a.refs[ref]
	a.mu.Unlock()
	if tr == nil {
		return
	}
	sendOff := tr.ackOff
	buf := make([]byte, chunk)
	eofSent := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.mu.Lock()
		if tr.rewind {
			// Hub demanded a restart from its high-water mark.
			sendOff = tr.ackOff
			tr.rewind = false
			eofSent = false
		}
		a.mu.Unlock()
		f, err := os.Open(tr.spool)
		if err != nil {
			// No output yet — poll until the Runner creates the file.
			if a.sleepOrDone(ctx, 300*time.Millisecond) {
				return
			}
			continue
		}
		n, rerr := f.ReadAt(buf, sendOff)
		f.Close()
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			a.mu.Lock()
			exited := tr.exited
			a.mu.Unlock()
			atEOF := errors.Is(rerr, io.EOF)
			msg := &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_LogChunk{LogChunk: &workerv1.LogChunk{
				Ref: ref, Offset: sendOff, Data: data, Eof: exited && atEOF,
			}}}
			select {
			case out <- msg:
				sendOff += int64(n)
				eofSent = exited && atEOF
			case <-ctx.Done():
				return
			}
			continue
		}
		a.mu.Lock()
		exited := tr.exited
		a.mu.Unlock()
		if exited {
			if !eofSent {
				msg := &workerv1.WorkerMessage{Msg: &workerv1.WorkerMessage_LogChunk{LogChunk: &workerv1.LogChunk{
					Ref: ref, Offset: sendOff, Eof: true,
				}}}
				select {
				case out <- msg:
					eofSent = true
				case <-ctx.Done():
					return
				}
			}
			return
		}
		if a.sleepOrDone(ctx, 300*time.Millisecond) {
			return
		}
	}
}

func (a *Agent) sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

// send is best-effort, for messages that are safe to drop (a resend or a
// periodic report will carry the same information later).
func send(out chan *workerv1.WorkerMessage, m *workerv1.WorkerMessage) {
	select {
	case out <- m:
	default:
	}
}

// sendReliable delivers a message that MUST NOT be silently dropped — the
// terminal TaskEvent above all: losing it leaves the hub believing the task
// is still running forever. It blocks (bounded per attempt), re-resolving the
// current session's queue so a reconnect mid-wait does not strand the event.
// Note: queue delivery still only reaches the hub if the session survives; a
// terminal event lost WITH its session is re-requested by the hub's Probe on
// the next Hello (the ref is absent from active_refs).
func (a *Agent) sendReliable(m *workerv1.WorkerMessage) {
	for i := 0; i < 120; i++ { // ~1 minute of attempts, then give up to the Probe path
		a.mu.Lock()
		out := a.out
		a.mu.Unlock()
		if out == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		select {
		case out <- m:
			return
		case <-time.After(500 * time.Millisecond):
			// queue jammed or session replaced — re-resolve and retry
		}
	}
	a.log.Warn("reliable send abandoned; hub will recover via reconnect probe")
}

// Shutdown asks the Runner to leave processes running for re-adoption.
func (a *Agent) Shutdown() { a.runner.Shutdown() }
