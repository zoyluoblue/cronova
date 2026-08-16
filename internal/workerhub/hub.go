// Package workerhub is the scheduler-side terminus of the dial-in worker
// protocol (proto/cronova/worker/v1): remote workers join with a one-time
// token, connect a single mTLS bidirectional Session stream, and receive task
// assignments over it. The hub presents each worker group as an
// executor.Executor, so the scheduler dispatches to a group exactly as it
// would to the local executor — and task logs stream back into the same
// per-run files the console already tails.
package workerhub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/store"
	workerv1 "github.com/zoyluo/cronova/proto/cronova/worker/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Options tune the hub's liveness windows.
type Options struct {
	Heartbeat time.Duration // expected worker heartbeat interval (default 5s)
	LostAfter time.Duration // silence before an offline worker is lost (default 3×Heartbeat)
	BootGrace time.Duration // after hub start, unknown refs probe as running (default 2×LostAfter)
	Logger    *slog.Logger
}

// Hub tracks connected workers and in-flight remote assignments.
type Hub struct {
	workerv1.UnimplementedWorkerHubServer

	store store.Store
	log   *slog.Logger
	opts  Options
	boot  time.Time

	mu       sync.Mutex
	sessions map[string]*session  // workerID → live stream
	refs     map[string]*refState // attempt ref → assignment
}

type session struct {
	workerID string
	labels   map[string]string
	draining bool
	active   int // hub-assigned refs currently on this worker
	send     chan *workerv1.ServerMessage
	done     chan struct{}
	lastSeen time.Time
}

type refState struct {
	workerID   string
	logPath    string // scheduler-local file log chunks append to
	outputPath string // scheduler-local $CRONOVA_OUTPUT destination
	logFile    *os.File
	logHW      int64 // high-water byte offset written
	phase      executor.Phase
	exitCode   int
	lost       bool
	doneAt     time.Time // when the ref went terminal (zero = still running)
}

// ErrNoWorker is returned by Launch when the target group has no live,
// non-draining worker. The dispatcher avoids this by checking GroupAvailable
// before admitting the task; hitting it means the last worker vanished in the
// gap, and the attempt fails over normally.
var ErrNoWorker = errors.New("workerhub: no online worker in group")

func New(st store.Store, opts Options) *Hub {
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 5 * time.Second
	}
	if opts.LostAfter <= 0 {
		opts.LostAfter = 3 * opts.Heartbeat
	}
	if opts.BootGrace <= 0 {
		opts.BootGrace = 2 * opts.LostAfter
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Hub{
		store:    st,
		log:      opts.Logger.With("comp", "workerhub"),
		opts:     opts,
		boot:     time.Now(),
		sessions: map[string]*session{},
		refs:     map[string]*refState{},
	}
}

// Run drives the liveness monitor until ctx ends: stale online workers go
// offline, silent-too-long workers go lost and their in-flight refs are
// released to fail over.
func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(h.opts.Heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.sweep(ctx)
		}
	}
}

func (h *Hub) sweep(ctx context.Context) {
	now := time.Now()
	h.mu.Lock()
	type lostRef struct{ ref, worker string }
	var lost []lostRef
	stale := map[string]bool{}
	for id, s := range h.sessions {
		if now.Sub(s.lastSeen) > h.opts.LostAfter {
			stale[id] = true // stream is up but silent — treat as dead (half-open TCP)
			close(s.done)
			delete(h.sessions, id)
		}
	}
	// GC terminal refs after a generous grace (the scheduler's poll reads the
	// terminal state within seconds; 30 minutes covers any recovery replay).
	// Without this the map grows for the process lifetime.
	for ref, rs := range h.refs {
		if rs.phase != executor.PhaseRunning && !rs.doneAt.IsZero() && now.Sub(rs.doneAt) > 30*time.Minute {
			rs.closeLogLocked()
			delete(h.refs, ref)
		}
	}
	// Refs whose worker has no session: grace for reconnects, then lost.
	// The store lookups happen OUTSIDE the lock (a slow database must not
	// freeze every Launch/Probe/log append), deduplicated per worker, with
	// re-validation after relocking.
	candidates := map[string]bool{}
	for _, rs := range h.refs {
		if rs.phase != executor.PhaseRunning || rs.lost {
			continue
		}
		if _, ok := h.sessions[rs.workerID]; ok {
			continue
		}
		candidates[rs.workerID] = true
	}
	h.mu.Unlock()

	silent := map[string]bool{}
	for id := range candidates {
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		silent[id] = stale[id] || h.workerSilentTooLong(qctx, id, now)
		cancel()
	}

	h.mu.Lock()
	for ref, rs := range h.refs {
		if rs.phase != executor.PhaseRunning || rs.lost || !silent[rs.workerID] {
			continue
		}
		if _, ok := h.sessions[rs.workerID]; ok {
			continue // reconnected while we were checking — keep it
		}
		rs.lost = true
		lost = append(lost, lostRef{ref, rs.workerID})
	}
	h.mu.Unlock()

	for id := range stale {
		_ = h.store.UpdateWorkerStatus(ctx, id, model.WorkerLost, 0, nil)
		h.log.Warn("worker lost (heartbeat silence)", "worker", id)
	}
	lostWorkers := map[string]bool{}
	for _, l := range lost {
		if !lostWorkers[l.worker] {
			lostWorkers[l.worker] = true
			// It disconnected holding in-flight work and never came back —
			// that is lost, not merely offline, and the console should say so.
			_ = h.store.UpdateWorkerStatus(ctx, l.worker, model.WorkerLost, 0, nil)
		}
		h.log.Warn("in-flight task released for failover: its worker is lost", "ref", l.ref, "worker", l.worker)
	}
}

// workerSilentTooLong consults the store for a disconnected worker's last
// heartbeat (the session is gone, so memory has nothing).
func (h *Hub) workerSilentTooLong(ctx context.Context, workerID string, now time.Time) bool {
	w, err := h.store.GetWorker(ctx, workerID)
	if err != nil {
		return true // unknown worker row — nothing to wait for
	}
	if w.State == model.WorkerLost {
		return true
	}
	if w.LastHeartbeat == nil {
		return now.Sub(h.boot) > h.opts.LostAfter
	}
	return now.Sub(*w.LastHeartbeat) > h.opts.LostAfter
}

// ---- gRPC service ----

// Session is the worker's long-lived stream. Identity is the mTLS client
// certificate CN; the first message must be a Hello that matches it.
func (h *Hub) Session(stream workerv1.WorkerHub_SessionServer) error {
	ctx := stream.Context()
	workerID, err := peerWorkerID(ctx)
	if err != nil {
		return err
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be Hello")
	}
	if hello.WorkerId != workerID {
		return status.Errorf(codes.PermissionDenied, "hello worker_id %q does not match certificate CN %q", hello.WorkerId, workerID)
	}
	w, err := h.store.GetWorker(ctx, workerID)
	if err != nil {
		return status.Errorf(codes.PermissionDenied, "worker %q is not registered (removed?)", workerID)
	}

	s := &session{
		workerID: workerID,
		labels:   hello.Labels,
		draining: w.Draining,
		send:     make(chan *workerv1.ServerMessage, 64),
		done:     make(chan struct{}),
		lastSeen: time.Now(),
	}
	h.mu.Lock()
	if old, ok := h.sessions[workerID]; ok {
		close(old.done) // a reconnect replaces the previous stream
	}
	h.sessions[workerID] = s
	// Re-adopt: refs the worker still runs stay running; refs we expected but
	// the worker no longer knows will answer Probe as UNKNOWN when asked.
	known := map[string]bool{}
	for _, ref := range hello.ActiveRefs {
		known[ref] = true
	}
	var orphans []string
	for _, ref := range hello.ActiveRefs {
		if _, tracked := h.refs[ref]; !tracked {
			orphans = append(orphans, ref) // hub restarted; rebuild bookkeeping below
		}
	}
	for ref, rs := range h.refs {
		if rs.workerID == workerID && rs.phase == executor.PhaseRunning {
			rs.lost = false
			// Restore the routing load counter: a reconnect replaces the
			// session object, and without this the worker would look idle to
			// least-loaded routing while still running its old assignments.
			s.active++
			if !known[ref] {
				// Ask the worker explicitly; it answers with a TaskEvent.
				select {
				case s.send <- &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_Probe{Probe: &workerv1.Probe{Ref: ref}}}:
				default:
				}
			}
		}
	}
	h.mu.Unlock()
	h.adoptOrphans(ctx, workerID, orphans)

	now := time.Now().UTC()
	w.Name, w.Labels, w.Version, w.State, w.LastHeartbeat = hello.Name, hello.Labels, hello.Version, model.WorkerOnline, &now
	if err := h.store.UpsertWorker(ctx, w); err != nil {
		h.log.Error("persist worker hello", "worker", workerID, "err", err)
	}
	h.log.Info("worker connected", "worker", workerID, "name", hello.Name, "group", w.Group(), "active_refs", len(hello.ActiveRefs))

	ack := &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_HelloAck{HelloAck: &workerv1.HelloAck{
		HeartbeatSeconds: int64(h.opts.Heartbeat / time.Second),
		ServerUnixMs:     time.Now().UnixMilli(),
	}}}
	if err := stream.Send(ack); err != nil {
		h.dropSession(s)
		return err
	}

	// Writer: single goroutine owns stream.Send.
	sendErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-s.done:
				sendErr <- nil
				return
			case msg := <-s.send:
				if err := stream.Send(msg); err != nil {
					sendErr <- err
					return
				}
			}
		}
	}()

	// Reader loop.
	var readErr error
	for {
		in, err := stream.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				readErr = err
			}
			break
		}
		h.touch(s)
		switch m := in.Msg.(type) {
		case *workerv1.WorkerMessage_Heartbeat:
			hb := time.Now().UTC()
			_ = h.store.UpdateWorkerStatus(ctx, workerID, model.WorkerOnline, int(m.Heartbeat.ActiveTasks), &hb)
		case *workerv1.WorkerMessage_TaskEvent:
			h.handleTaskEvent(workerID, m.TaskEvent)
		case *workerv1.WorkerMessage_LogChunk:
			h.handleLogChunk(s, m.LogChunk)
		}
	}
	h.dropSession(s)
	<-sendErr
	_ = h.store.UpdateWorkerStatus(context.WithoutCancel(ctx), workerID, model.WorkerOffline, 0, nil)
	h.log.Info("worker disconnected", "worker", workerID, "err", readErr)
	return readErr
}

func (h *Hub) touch(s *session) {
	h.mu.Lock()
	s.lastSeen = time.Now()
	h.mu.Unlock()
}

func (h *Hub) dropSession(s *session) {
	h.mu.Lock()
	if cur, ok := h.sessions[s.workerID]; ok && cur == s {
		delete(h.sessions, s.workerID)
		close(s.done)
	}
	h.mu.Unlock()
}

// handleTaskEvent records an attempt transition. Output (remote XCom) is
// written to the scheduler-local $CRONOVA_OUTPUT path BEFORE the phase flips
// to exited, so collectTaskOutput always finds it.
func (h *Hub) handleTaskEvent(workerID string, ev *workerv1.TaskEvent) {
	h.mu.Lock()
	rs, ok := h.refs[ev.Ref]
	if !ok || rs.workerID != workerID {
		h.mu.Unlock()
		h.log.Warn("task event for unknown ref", "ref", ev.Ref, "worker", workerID)
		return
	}
	outPath := rs.outputPath
	h.mu.Unlock()

	if len(ev.Output) > 0 && outPath != "" {
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err == nil {
			if err := os.WriteFile(outPath, ev.Output, 0o644); err != nil {
				h.log.Error("write remote task output", "ref", ev.Ref, "err", err)
			}
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	switch ev.Phase {
	case workerv1.Phase_PHASE_RUNNING:
		rs.phase = executor.PhaseRunning
	case workerv1.Phase_PHASE_EXITED:
		if rs.phase != executor.PhaseExited {
			if s, ok := h.sessions[workerID]; ok && s.active > 0 {
				s.active--
			}
		}
		rs.phase = executor.PhaseExited
		rs.exitCode = int(ev.ExitCode)
		rs.doneAt = time.Now()
		rs.closeLogLocked()
	case workerv1.Phase_PHASE_UNKNOWN:
		// The worker has no record (it restarted and could not re-adopt).
		rs.phase = executor.PhaseUnknown
		rs.doneAt = time.Now()
		rs.closeLogLocked()
	}
	if ev.LaunchError != "" {
		h.log.Error("remote launch failed", "ref", ev.Ref, "worker", workerID, "err", ev.LaunchError)
		rs.phase = executor.PhaseExited
		rs.exitCode = 127
		rs.doneAt = time.Now()
		rs.closeLogLocked()
	}
}

// handleLogChunk appends streamed task output to the scheduler-local log file
// the console already tails. Chunks are idempotent by offset: anything at or
// below the high-water mark is dropped, so resends after reconnect are safe.
func (h *Hub) handleLogChunk(s *session, c *workerv1.LogChunk) {
	h.mu.Lock()
	rs, ok := h.refs[c.Ref]
	if !ok || rs.workerID != s.workerID {
		h.mu.Unlock()
		return
	}
	if c.Offset < rs.logHW { // overlap — drop the already-written prefix
		if int64(len(c.Data)) <= rs.logHW-c.Offset {
			h.ackLocked(s, c.Ref, rs.logHW)
			h.mu.Unlock()
			return
		}
		c.Data = c.Data[rs.logHW-c.Offset:]
		c.Offset = rs.logHW
	}
	if c.Offset > rs.logHW {
		// Gap — a chunk was lost mid-stream (should not happen inside one
		// stream; can after an ill-timed reconnect, or when the hub-side log
		// was pruned). The explicit rewind flag orders the worker to restart
		// from our high-water mark — a plain re-ack would be ignored (workers
		// never move their cursor backward on ordinary acks).
		h.rewindLocked(s, c.Ref, rs.logHW)
		h.mu.Unlock()
		return
	}
	if rs.logFile == nil && rs.logPath != "" {
		_ = os.MkdirAll(filepath.Dir(rs.logPath), 0o755)
		f, err := os.OpenFile(rs.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			h.mu.Unlock()
			h.log.Error("open remote task log", "ref", c.Ref, "err", err)
			return
		}
		rs.logFile = f
	}
	if rs.logFile != nil && len(c.Data) > 0 {
		if _, err := rs.logFile.Write(c.Data); err != nil {
			h.log.Error("append remote task log", "ref", c.Ref, "err", err)
		} else {
			rs.logHW += int64(len(c.Data))
		}
	}
	if c.Eof {
		rs.closeLogLocked()
	}
	h.ackLocked(s, c.Ref, rs.logHW)
	h.mu.Unlock()
}

func (rs *refState) closeLogLocked() {
	if rs.logFile != nil {
		_ = rs.logFile.Close()
		rs.logFile = nil
	}
}

func (h *Hub) ackLocked(s *session, ref string, offset int64) {
	msg := &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_LogAck{LogAck: &workerv1.LogAck{Ref: ref, Offset: offset}}}
	select {
	case s.send <- msg:
	default: // ack channel full — the next chunk re-acks
	}
}

func (h *Hub) rewindLocked(s *session, ref string, offset int64) {
	msg := &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_LogAck{LogAck: &workerv1.LogAck{Ref: ref, Offset: offset, Rewind: true}}}
	select {
	case s.send <- msg:
	default: // dropped rewind re-fires on the worker's next gap chunk
	}
}

// adoptOrphans rebuilds bookkeeping for attempts a reconnecting worker still
// runs but this hub process has never seen (the scheduler restarted). The log
// destination and $CRONOVA_OUTPUT path are re-derived from the task-instance
// row, so log streaming and remote XCom survive the restart.
func (h *Hub) adoptOrphans(ctx context.Context, workerID string, refs []string) {
	for _, ref := range refs {
		runID, taskID, try, ok := splitRef(ref)
		if !ok {
			continue
		}
		var logPath string
		if tis, err := h.store.ListTaskInstances(ctx, runID); err == nil {
			for _, ti := range tis {
				if ti.TaskID == taskID && ti.ExecutorRef == ref {
					logPath = ti.LogPath
					break
				}
			}
		}
		h.mu.Lock()
		if _, tracked := h.refs[ref]; !tracked {
			rs := &refState{workerID: workerID, logPath: logPath, phase: executor.PhaseRunning}
			if logPath != "" {
				rs.outputPath = executor.OutputPath(logPath, try)
				// Resume the log high-water mark from what already reached disk,
				// so the worker's from-zero resend dedups instead of duplicating.
				if fi, err := os.Stat(logPath); err == nil {
					rs.logHW = fi.Size()
				}
			}
			h.refs[ref] = rs
			if s, ok := h.sessions[workerID]; ok {
				s.active++
			}
		}
		h.mu.Unlock()
		h.log.Info("adopted in-flight remote attempt", "ref", ref, "worker", workerID)
	}
}

// splitRef parses an attempt ref (run_id/task_id/try).
func splitRef(ref string) (runID, taskID string, try int, ok bool) {
	i := strings.Index(ref, "/")
	j := strings.LastIndex(ref, "/")
	if i <= 0 || j <= i {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(ref[j+1:])
	if err != nil {
		return "", "", 0, false
	}
	return ref[:i], ref[i+1 : j], n, true
}

// Owns reports whether ref is a hub-assigned attempt (routing key for the
// scheduler's composite executor).
func (h *Hub) Owns(ref string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.refs[ref]
	return ok
}

// InBootGrace reports whether the hub is still inside its post-start window
// where unknown refs may belong to workers that have not reconnected yet.
func (h *Hub) InBootGrace() bool { return time.Since(h.boot) < h.opts.BootGrace }

// ProbeRef and CancelRef are the ref-addressed forms used by the scheduler's
// routing executor (group is irrelevant once a ref is assigned).
func (h *Hub) ProbeRef(ctx context.Context, ref string) (executor.Status, error) {
	return groupExec{h: h}.Probe(ctx, ref)
}

func (h *Hub) CancelRef(ctx context.Context, ref string) error {
	return groupExec{h: h}.Cancel(ctx, ref)
}

// peerWorkerID extracts the mTLS client certificate CN.
func peerWorkerID(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no peer")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
		return "", status.Error(codes.Unauthenticated, "client certificate required")
	}
	cn := tlsInfo.State.PeerCertificates[0].Subject.CommonName
	if cn == "" {
		return "", status.Error(codes.Unauthenticated, "client certificate has no CN")
	}
	return cn, nil
}

// ---- executor adapter ----

// GroupAvailable reports whether a group has at least one live, non-draining
// worker — the dispatcher holds a task back (it stays scheduled) when false,
// so a temporarily empty group queues instead of failing.
func (h *Hub) GroupAvailable(group string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if sessionGroup(s) == group && !s.draining {
			return true
		}
	}
	return false
}

func sessionGroup(s *session) string {
	if g := s.labels["group"]; g != "" {
		return g
	}
	return "default"
}

// SetDraining flips a connected session's draining flag (the store row is
// updated by the API handler; this covers the live routing view).
func (h *Hub) SetDraining(workerID string, draining bool) {
	h.mu.Lock()
	if s, ok := h.sessions[workerID]; ok {
		s.draining = draining
	}
	h.mu.Unlock()
}

// Disconnect closes a worker's live session (used by remove).
func (h *Hub) Disconnect(workerID string) {
	h.mu.Lock()
	if s, ok := h.sessions[workerID]; ok {
		close(s.done)
		delete(h.sessions, workerID)
	}
	h.mu.Unlock()
}

// ExecutorForGroup returns the executor.Executor view of one worker group.
func (h *Hub) ExecutorForGroup(group string) executor.Executor {
	return groupExec{h: h, group: group}
}

type groupExec struct {
	h     *Hub
	group string
}

// Launch assigns the task to the least-loaded live worker of the group.
func (g groupExec) Launch(ctx context.Context, spec executor.Spec) (string, error) {
	h := g.h
	h.mu.Lock()
	if rs, ok := h.refs[spec.TaskRunID]; ok && !rs.lost && rs.phase != executor.PhaseUnknown {
		h.mu.Unlock()
		return spec.TaskRunID, nil // idempotent relaunch
	}
	var best *session
	for _, s := range h.sessions {
		if sessionGroup(s) != g.group || s.draining {
			continue
		}
		if best == nil || s.active < best.active {
			best = s
		}
	}
	if best == nil {
		h.mu.Unlock()
		return "", fmt.Errorf("%w %q", ErrNoWorker, g.group)
	}
	h.refs[spec.TaskRunID] = &refState{
		workerID:   best.workerID,
		logPath:    spec.LogPath,
		outputPath: spec.Env["CRONOVA_OUTPUT"],
		phase:      executor.PhaseRunning,
	}
	best.active++
	assign := &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_Assign{Assign: &workerv1.Assign{
		Ref:            spec.TaskRunID,
		Type:           spec.Type,
		Command:        spec.Command,
		Env:            spec.Env,
		TimeoutSeconds: int64(spec.Timeout / time.Second),
		Redact:         spec.Redact,
	}}}
	workerID := best.workerID
	select {
	case best.send <- assign:
	default:
		// Send queue jammed — treat as unavailable rather than blocking dispatch.
		delete(h.refs, spec.TaskRunID)
		best.active--
		h.mu.Unlock()
		return "", fmt.Errorf("workerhub: worker %s send queue full", workerID)
	}
	h.mu.Unlock()
	h.log.Info("task assigned", "ref", spec.TaskRunID, "worker", workerID, "group", g.group)
	return spec.TaskRunID, nil
}

// Probe reports a remote attempt's state. A ref on a lost worker probes as
// UNKNOWN so the scheduler's normal lost-task path (fail + retry policy)
// fails it over; unknown refs during the post-boot grace probe as running to
// give reconnecting workers time to claim them.
func (g groupExec) Probe(ctx context.Context, ref string) (executor.Status, error) {
	h := g.h
	h.mu.Lock()
	defer h.mu.Unlock()
	rs, ok := h.refs[ref]
	if !ok {
		if time.Since(h.boot) < h.opts.BootGrace {
			return executor.Status{Phase: executor.PhaseRunning}, nil
		}
		return executor.Status{Phase: executor.PhaseUnknown}, nil
	}
	if rs.lost {
		return executor.Status{Phase: executor.PhaseUnknown}, nil
	}
	return executor.Status{Phase: rs.phase, ExitCode: rs.exitCode}, nil
}

// Cancel forwards a kill to the owning worker.
func (g groupExec) Cancel(ctx context.Context, ref string) error {
	h := g.h
	h.mu.Lock()
	defer h.mu.Unlock()
	rs, ok := h.refs[ref]
	if !ok {
		return nil
	}
	s, ok := h.sessions[rs.workerID]
	if !ok {
		return fmt.Errorf("workerhub: worker %s not connected", rs.workerID)
	}
	msg := &workerv1.ServerMessage{Msg: &workerv1.ServerMessage_Cancel{Cancel: &workerv1.Cancel{Ref: ref}}}
	select {
	case s.send <- msg:
		return nil
	default:
		return fmt.Errorf("workerhub: worker %s send queue full", rs.workerID)
	}
}

// Forget drops a terminal ref's bookkeeping (called opportunistically; the
// map would otherwise grow for the process lifetime).
func (h *Hub) Forget(ref string) {
	h.mu.Lock()
	if rs, ok := h.refs[ref]; ok && rs.phase != executor.PhaseRunning {
		rs.closeLogLocked()
		delete(h.refs, ref)
	}
	h.mu.Unlock()
}
