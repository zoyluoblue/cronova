// Package scheduler is cronova's core: it loads DAGs, creates runs (on a cron
// schedule or on demand), and drives task instances through their state machine
// each tick — dispatching ready tasks, propagating failures, and finalizing
// runs. See docs/ARCHITECTURE.md §7.
//
// M1 executes tasks in-process via an executor.Executor running in a goroutine
// per task. The scheduler loop is single-goroutine; tasks coordinate back
// through the store (the single source of truth). Pool enforcement, retries,
// cross-DAG dependency triggers, and catchup arrive in M3/M4.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/zoyluo/cronova/internal/executor"
	"github.com/zoyluo/cronova/internal/model"
	"github.com/zoyluo/cronova/internal/scheduler/parser"
	"github.com/zoyluo/cronova/internal/store"
)

// Options configures a Scheduler.
type Options struct {
	DagDir       string        // directory of *.yaml DAG definitions ("" = none)
	LogDir       string        // root directory for per-task log files
	Tick         time.Duration // scheduling loop interval
	PollInterval time.Duration // how often a running task is Probed for completion
	Logger       *slog.Logger
	// AllowPrivateNotifyTargets disables the SSRF guard on outbound notify
	// webhooks (loopback/private/link-local IPs). Off in production; tests set it
	// so an httptest server on 127.0.0.1 can receive deliveries.
	AllowPrivateNotifyTargets bool
}

// Scheduler is the cronova scheduling engine.
type Scheduler struct {
	store store.Store
	exec  executor.Executor
	opts  Options
	log   *slog.Logger

	mu        sync.Mutex
	dags      map[string]*model.DAG
	schedules map[string]cron.Schedule

	notifyClient *http.Client // hardened outbound client for notify webhooks

	// finalizeMu serializes a tick's processRun against the manual mark ops
	// (MarkTask/MarkRun), which run on API goroutines. Without it the tick can
	// finalize a run from a task snapshot taken before a concurrent mark, then
	// write that stale outcome over the mark (there is no CAS on the run row) —
	// wedging the run terminal while a just-released task sits scheduled.
	finalizeMu     sync.Mutex
	notifySuppress map[string]bool // run ids whose next (mark-driven) re-finalize must not re-notify
	slaAlerted     map[string]bool // dedup keys ("run" / "run/task") already SLA-alerted, cleared at finalize

	bootTime time.Time
	inflight sync.WaitGroup
}

// New constructs a Scheduler.
func New(st store.Store, ex executor.Executor, opts Options) *Scheduler {
	if opts.Tick <= 0 {
		opts.Tick = 2 * time.Second
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 250 * time.Millisecond
	}
	if opts.LogDir == "" {
		opts.LogDir = "logs"
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return &Scheduler{
		store:          st,
		exec:           ex,
		opts:           opts,
		log:            opts.Logger,
		dags:           map[string]*model.DAG{},
		schedules:      map[string]cron.Schedule{},
		notifyClient:   newNotifyClient(opts.AllowPrivateNotifyTargets),
		notifySuppress: map[string]bool{},
		slaAlerted:     map[string]bool{},
		bootTime:       time.Now().UTC(),
	}
}

// LoadDAGs reads, parses, validates, and registers every *.yaml/*.yml file in
// the configured DagDir. Bad files are logged and skipped, not fatal.
func (s *Scheduler) LoadDAGs(ctx context.Context) error {
	if s.opts.DagDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.opts.DagDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.opts.DagDir, name))
		if err != nil {
			s.log.Error("read dag file", "file", name, "err", err)
			continue
		}
		d, err := parser.Parse(raw)
		if err != nil {
			s.log.Error("parse dag", "file", name, "err", err)
			continue
		}
		// The DB archive wins over a lingering file: if this dag_id was
		// soft-deleted, do NOT re-register it (which would revive it). This makes
		// a delete durable even if the YAML file removal failed or was restored.
		if existing, gerr := s.store.GetDAG(ctx, d.DagID); gerr == nil && existing.DeletedAt != nil {
			s.log.Warn("skipping soft-deleted dag (file still present)", "dag", d.DagID, "file", name)
			continue
		}
		if err := s.registerDAG(ctx, d); err != nil {
			s.log.Error("register dag", "dag", d.DagID, "err", err)
		}
	}
	return nil
}

const defaultPoolSlots = 16

func (s *Scheduler) registerDAG(ctx context.Context, d *model.DAG) error {
	if err := s.store.UpsertDAG(ctx, d); err != nil {
		return err
	}
	if err := s.ensurePools(ctx, d); err != nil {
		return err
	}
	if err := s.store.ReplaceDagDependencies(ctx, d.DagID, d.TriggerAfter); err != nil {
		return err
	}
	var sched cron.Schedule
	if d.Schedule != "" {
		sc, err := parser.ParseSchedule(d.Schedule)
		if err != nil {
			return err
		}
		sched = sc
	}
	s.mu.Lock()
	s.dags[d.DagID] = d
	if sched != nil {
		s.schedules[d.DagID] = sched
	} else {
		delete(s.schedules, d.DagID)
	}
	s.mu.Unlock()
	return nil
}

// CreateDAG validates a YAML definition, persists it to the DAG directory (so
// it survives restarts and is the source of truth), and registers it live.
func (s *Scheduler) CreateDAG(ctx context.Context, yamlText string) (string, error) {
	d, err := parser.Parse([]byte(yamlText))
	if err != nil {
		return "", err
	}
	if s.opts.DagDir != "" {
		if err := os.MkdirAll(s.opts.DagDir, 0o755); err != nil {
			return "", err
		}
		path := filepath.Join(s.opts.DagDir, d.DagID+".yaml")
		if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
			return "", fmt.Errorf("write dag file: %w", err)
		}
	}
	if err := s.registerDAG(ctx, d); err != nil {
		return "", err
	}
	s.log.Info("dag created", "dag", d.DagID)
	return d.DagID, nil
}

// DeleteDAG soft-deletes (archives) a DAG. It refuses if the DAG has any
// queued/running run (so we never orphan an in-flight executor process), then
// marks it deleted, evicts it from the live cache (so it is never scheduled or
// triggered), and removes its YAML file (the definition is preserved in the
// dags row for recovery). Run history is kept.
func (s *Scheduler) DeleteDAG(ctx context.Context, dagID string) error {
	active, err := s.store.CountActiveRuns(ctx, dagID)
	if err != nil {
		return err
	}
	if active > 0 {
		return fmt.Errorf("dag %q: %w (%d queued/running)", dagID, model.ErrActiveRuns, active)
	}
	if err := s.store.SoftDeleteDAG(ctx, dagID); err != nil {
		return err // ErrNotFound if absent or already deleted
	}
	s.mu.Lock()
	delete(s.dags, dagID)
	delete(s.schedules, dagID)
	s.mu.Unlock()
	if s.opts.DagDir != "" {
		// Remove the on-disk file so a restart's LoadDAGs won't re-register the
		// archived DAG; the definition stays in the dags row. A failed removal is
		// not fatal (LoadDAGs also skips soft-deleted rows), but log it.
		for _, ext := range []string{".yaml", ".yml"} {
			if err := os.Remove(filepath.Join(s.opts.DagDir, dagID+ext)); err != nil && !os.IsNotExist(err) {
				s.log.Warn("could not remove dag file (archive still enforced via DB)", "dag", dagID, "err", err)
			}
		}
	}
	s.log.Info("dag archived (soft-deleted)", "dag", dagID)
	return nil
}

// NextSchedule returns a human-ish description of a DAG's next scheduled fire,
// or "" if it has no schedule. Paused/manual cases are handled by the caller.
func (s *Scheduler) NextSchedule(ctx context.Context, d *model.DAG) (time.Time, bool) {
	if d.Schedule == "" {
		return time.Time{}, false
	}
	// A 0-task shell is never scheduled, so it has no "next fire". The store-row
	// DAG passed here has no parsed Tasks, so consult the cache (registered DAGs
	// carry their parsed task set).
	if cd, _, ok := s.cachedDAG(d.DagID); ok && len(cd.Tasks) == 0 {
		return time.Time{}, false
	}
	sched, err := parser.ParseSchedule(d.Schedule)
	if err != nil {
		return time.Time{}, false
	}
	return sched.Next(s.scheduleAnchor(ctx, d)), true
}

// ensurePools auto-provisions any pool a task references but that does not yet
// exist (with a default slot count). Existing pools keep their configured size.
func (s *Scheduler) ensurePools(ctx context.Context, d *model.DAG) error {
	seen := map[string]bool{}
	for _, t := range d.Tasks {
		if t.Pool == "" || seen[t.Pool] {
			continue
		}
		seen[t.Pool] = true
		_, err := s.store.GetPool(ctx, t.Pool)
		if err == nil {
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := s.store.UpsertPool(ctx, &model.Pool{Name: t.Pool, Slots: defaultPoolSlots}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) poolSlots(ctx context.Context, name string) int {
	p, err := s.store.GetPool(ctx, name)
	if err != nil {
		return defaultPoolSlots
	}
	return p.Slots
}

func (s *Scheduler) cachedDAG(id string) (*model.DAG, cron.Schedule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.dags[id]
	return d, s.schedules[id], ok
}

func (s *Scheduler) allDAGs() []*model.DAG {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*model.DAG, 0, len(s.dags))
	for _, d := range s.dags {
		out = append(out, d)
	}
	return out
}

// TriggerManual creates a manual run for dagID; the running loop picks it up on
// the next tick. Returns the new run id.
func (s *Scheduler) TriggerManual(ctx context.Context, dagID string, params map[string]string) (string, error) {
	// Only an actively-registered DAG can be triggered: a soft-deleted DAG is
	// evicted from the cache, and an unknown one was never registered. Using the
	// cache also gives the parsed task set, so we can gate on the real task count
	// — a 0-task shell would otherwise finalize instantly as a phantom success.
	d, _, ok := s.cachedDAG(dagID)
	if !ok {
		return "", fmt.Errorf("dag %q not found or not active: %w", dagID, store.ErrNotFound)
	}
	if len(d.Tasks) == 0 {
		return "", fmt.Errorf("dag %q: %w; add a task before triggering", dagID, model.ErrNoTasks)
	}
	now := time.Now().UTC()
	run := &model.DagRun{
		RunID:       fmt.Sprintf("%s__manual_%d", dagID, now.UnixNano()),
		DagID:       dagID,
		LogicalDate: now,
		State:       model.RunQueued,
		TriggerType: model.TriggerManual,
		Params:      params,
	}
	if err := s.store.CreateDagRun(ctx, run); err != nil {
		return "", err
	}
	s.log.Info("manual run created", "dag", dagID, "run", run.RunID)
	return run.RunID, nil
}

// CancelRun stops an active run: mark it cancelled first (so in-flight polling
// goroutines observe it and don't overwrite the outcome), then kill each running
// task and mark every non-terminal task cancelled.
func (s *Scheduler) CancelRun(ctx context.Context, runID string) error {
	// Serialize with the tick's finalize (see finalizeMu): every run-state-mutating
	// op reads state and writes under this lock so a stale-snapshot finalize can
	// never clobber it (UpdateDagRunState has no CAS on the run row).
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	run, err := s.store.GetDagRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.State != model.RunQueued && run.State != model.RunRunning {
		return fmt.Errorf("run %q (%s): %w", runID, run.State, model.ErrRunNotActive)
	}
	fin := time.Now().UTC()
	// leave started_at NULL if the run never left queued — an honest "cancelled
	// before start" (no spurious 0s duration).
	if err := s.store.UpdateDagRunState(ctx, runID, model.RunCancelled, run.StartedAt, &fin); err != nil {
		return err
	}
	tis, err := s.store.ListTaskInstances(ctx, runID)
	if err != nil {
		return err
	}
	for _, ti := range tis {
		if ti.State.IsTerminal() {
			continue
		}
		if ti.ExecutorRef != "" {
			_ = s.exec.Cancel(ctx, ti.ExecutorRef) // best-effort kill of the running process
		}
		ti.State = model.TaskCancelled
		ti.FinishedAt = &fin
		if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
			s.log.Error("cancel task", "ti", ti.ID, "err", err)
		}
	}
	s.clearSLAKeys(runID) // cancelling is a terminal transition — drop any SLA-dedup entries
	s.log.Info("run cancelled", "run", runID)
	return nil
}

// RetryTask clears a task and everything downstream of it back to scheduled and
// reactivates the run, so the scheduler re-runs that subtree.
func (s *Scheduler) RetryTask(ctx context.Context, runID, taskID string) error {
	s.finalizeMu.Lock() // serialize the terminal-gate + reactivation with the tick's finalize
	defer s.finalizeMu.Unlock()
	run, d, tiByTask, err := s.retryContext(ctx, runID)
	if err != nil {
		return err
	}
	valid := taskIDSet(d.Tasks)
	if tiByTask[taskID] == nil || !valid[taskID] {
		return fmt.Errorf("task %q in run %q: %w", taskID, runID, store.ErrNotFound)
	}
	// intersect with the current DAG's tasks: a task no longer in the DAG has no
	// dispatch path, so reactivating it would wedge the run in `running` forever.
	ids := intersect(downstreamClosure(d.Tasks, taskID), valid)
	return s.clearAndReactivate(ctx, run, tiByTask, ids)
}

// RetryRun clears every failed / upstream_failed / cancelled task (and its
// downstream) and reactivates the run.
func (s *Scheduler) RetryRun(ctx context.Context, runID string) error {
	s.finalizeMu.Lock() // serialize the terminal-gate + reactivation with the tick's finalize
	defer s.finalizeMu.Unlock()
	run, d, tiByTask, err := s.retryContext(ctx, runID)
	if err != nil {
		return err
	}
	valid := taskIDSet(d.Tasks)
	ids := map[string]bool{}
	for _, ti := range tiByTask {
		if !valid[ti.TaskID] {
			continue // orphan instance (task removed from the DAG since the run)
		}
		switch ti.State {
		case model.TaskFailed, model.TaskUpstreamFailed, model.TaskCancelled, model.TaskTimedOut:
			for id := range downstreamClosure(d.Tasks, ti.TaskID) {
				if valid[id] {
					ids[id] = true
				}
			}
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("run %q: %w", runID, model.ErrNothingToRetry)
	}
	return s.clearAndReactivate(ctx, run, tiByTask, ids)
}

var markableTaskStates = map[model.TaskState]bool{
	model.TaskSuccess: true, model.TaskFailed: true, model.TaskSkipped: true,
}

var markableRunStates = map[model.RunState]bool{
	model.RunSuccess: true, model.RunFailed: true,
}

// MarkTask forces a single task instance to a chosen terminal state (success,
// failed, or skipped) and re-drives the run. It works on an active run too: a
// still-running task's process is killed first, and the state is written
// unguarded so the override wins over a concurrent natural finalize (which then
// no-ops via its guarded write). Marking to a non-blocking state (success or
// skipped) releases downstream tasks that were upstream_failed because of it.
func (s *Scheduler) MarkTask(ctx context.Context, runID, taskID string, target model.TaskState) error {
	if !markableTaskStates[target] {
		return fmt.Errorf("task state %q: %w", target, model.ErrBadMarkState)
	}
	// Under finalizeMu the run/task snapshot loaded here is atomic w.r.t. the tick's
	// finalize: whether the tick finalizes just before or just after us, we observe
	// the true current run.State and react correctly (reactivate a just-finalized
	// run; leave an active one for the tick to re-drive).
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	run, d, tiByTask, err := s.markContext(ctx, runID)
	if err != nil {
		return err
	}
	valid := taskIDSet(d.Tasks)
	ti := tiByTask[taskID]
	if ti == nil || !valid[taskID] {
		return fmt.Errorf("task %q in run %q: %w", taskID, runID, store.ErrNotFound)
	}
	now := time.Now().UTC()
	// 1. kill a still-running/queued process so it can't keep executing or writing.
	if !ti.State.IsTerminal() && ti.ExecutorRef != "" {
		_ = s.exec.Cancel(ctx, ti.ExecutorRef)
	}
	// 2. release downstream BEFORE flipping the marked task — the reset tasks keep
	// the run non-terminal during the swap, so a concurrent tick can't finalize (and
	// re-notify) a half-marked run. Only a non-blocking target unblocks downstream.
	if target == model.TaskSuccess || target == model.TaskSkipped {
		closure := intersect(downstreamClosure(d.Tasks, taskID), valid)
		for id := range closure {
			dt := tiByTask[id]
			if id == taskID || dt == nil || dt.State != model.TaskUpstreamFailed {
				continue
			}
			dt.State = model.TaskScheduled
			dt.ExecutorRef = ""
			dt.StartedAt = nil
			dt.FinishedAt = nil
			if err := s.store.UpdateTaskInstance(ctx, dt); err != nil {
				return err
			}
		}
	}
	// 3. set the marked task (unguarded: the override must win; once terminal, the
	// poll goroutine's guarded finalize no-ops). started_at stays as-is — a task that
	// never ran keeps a NULL start (honest, no fabricated duration).
	ti.State = target
	ti.FinishedAt = &now
	if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
		return err
	}
	// 4. a terminal run must be reactivated so the tick re-drives (dispatch released
	// downstream) and re-finalizes; an active run is already being driven. Suppress
	// the re-finalize's notify — the operator already saw this run finish once.
	if run.State.IsTerminal() {
		s.notifySuppress[runID] = true
		// reset the clock (see clearAndReactivate): a reactivated run is a fresh
		// deadline window, else a timed_out run marked+reactivated re-times-out at once.
		if err := s.store.UpdateDagRunState(ctx, runID, model.RunRunning, &now, nil); err != nil {
			return err
		}
	}
	s.log.Info("task marked", "run", runID, "task", taskID, "state", target)
	return nil
}

// MarkRun overrides a FINISHED run's recorded outcome (success or failed). It is
// refused on an active run: the tick derives an active run's state from its task
// states and would immediately overwrite the override — cancel the run or mark
// its tasks instead. Marking success also fires any downstream-DAG triggers, as a
// natural success would (task states are left untouched: this is a recorded
// outcome override, not a re-run).
func (s *Scheduler) MarkRun(ctx context.Context, runID string, target model.RunState) error {
	if !markableRunStates[target] {
		return fmt.Errorf("run state %q: %w", target, model.ErrBadMarkState)
	}
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	run, err := s.store.GetDagRun(ctx, runID)
	if err != nil {
		return err
	}
	if !run.State.IsTerminal() {
		return fmt.Errorf("run %q (%s): %w", runID, run.State, model.ErrRunStillActive)
	}
	if run.State == target {
		return nil // idempotent
	}
	fin := run.FinishedAt
	if fin == nil {
		now := time.Now().UTC() // never nil for a terminal run, but be defensive
		fin = &now
	}
	if err := s.store.UpdateDagRunState(ctx, runID, target, run.StartedAt, fin); err != nil {
		return err
	}
	if target == model.RunSuccess {
		s.triggerDownstreams(ctx, run)
	}
	s.log.Info("run marked", "run", runID, "state", target)
	return nil
}

// markContext loads the run, its (active) DAG, and task instances by task id.
// Unlike retryContext it does NOT refuse an active run: marking a single task is
// safe on a running run (kill + guarded-write protection), and killing a running
// task is exactly the point of the "mark a running task" capability.
func (s *Scheduler) markContext(ctx context.Context, runID string) (*model.DagRun, *model.DAG, map[string]*model.TaskInstance, error) {
	run, err := s.store.GetDagRun(ctx, runID)
	if err != nil {
		return nil, nil, nil, err
	}
	d, _, ok := s.cachedDAG(run.DagID)
	if !ok {
		return nil, nil, nil, fmt.Errorf("dag %q not active: %w", run.DagID, store.ErrNotFound)
	}
	tis, err := s.store.ListTaskInstances(ctx, runID)
	if err != nil {
		return nil, nil, nil, err
	}
	byTask := make(map[string]*model.TaskInstance, len(tis))
	for _, ti := range tis {
		byTask[ti.TaskID] = ti
	}
	return run, d, byTask, nil
}

// retryContext loads the run, its (active) DAG, and its task instances by task id.
// Retry is refused on a still-active run: a running run has in-flight task
// goroutines, and clearing a running task would orphan its process and race its
// finalize. Cancel first, then retry.
func (s *Scheduler) retryContext(ctx context.Context, runID string) (*model.DagRun, *model.DAG, map[string]*model.TaskInstance, error) {
	run, err := s.store.GetDagRun(ctx, runID)
	if err != nil {
		return nil, nil, nil, err
	}
	if run.State == model.RunQueued || run.State == model.RunRunning {
		return nil, nil, nil, fmt.Errorf("run %q (%s): %w", runID, run.State, model.ErrRunStillActive)
	}
	d, _, ok := s.cachedDAG(run.DagID)
	if !ok {
		return nil, nil, nil, fmt.Errorf("dag %q not active: %w", run.DagID, store.ErrNotFound)
	}
	tis, err := s.store.ListTaskInstances(ctx, runID)
	if err != nil {
		return nil, nil, nil, err
	}
	byTask := make(map[string]*model.TaskInstance, len(tis))
	for _, ti := range tis {
		byTask[ti.TaskID] = ti
	}
	return run, d, byTask, nil
}

func taskIDSet(tasks []model.Task) map[string]bool {
	m := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		m[t.ID] = true
	}
	return m
}

func intersect(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

// clearAndReactivate resets the named tasks to scheduled (fresh attempt count)
// and flips the run back to running so the tick loop re-dispatches them.
func (s *Scheduler) clearAndReactivate(ctx context.Context, run *model.DagRun, tiByTask map[string]*model.TaskInstance, ids map[string]bool) error {
	for id := range ids {
		ti := tiByTask[id]
		if ti == nil {
			continue
		}
		ti.State = model.TaskScheduled
		ti.ExecutorRef = ""
		ti.StartedAt = nil
		ti.FinishedAt = nil
		// keep TryNumber accumulating (do NOT reset to 0): the next dispatch derives
		// the executor ref from it (attemptRef = runID/task/try), and Launch is
		// idempotent per ref — reusing an old ref would return the stale killed/failed
		// result instead of running a fresh attempt.
		if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
			return err
		}
	}
	// Restart the clock: a reactivated run is a fresh execution window, so SLA and
	// dagrun_timeout measure from now — otherwise a run that already breached its
	// deadline (e.g. a timed_out run being retried) would re-timeout on the very next
	// tick and never make progress.
	now := time.Now().UTC()
	// finished_at cleared → the run is active again and processActiveRuns picks it up.
	return s.store.UpdateDagRunState(ctx, run.RunID, model.RunRunning, &now, nil)
}

// downstreamClosure returns taskID plus every task transitively downstream of it.
func downstreamClosure(tasks []model.Task, taskID string) map[string]bool {
	dependents := map[string][]string{}
	for _, t := range tasks {
		for _, dep := range t.Deps {
			dependents[dep] = append(dependents[dep], t.ID)
		}
	}
	closure := map[string]bool{}
	queue := []string{taskID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if closure[id] {
			continue
		}
		closure[id] = true
		queue = append(queue, dependents[id]...)
	}
	return closure
}

// Run drives the scheduling loop until ctx is cancelled, then waits for any
// in-flight tasks to finish (graceful shutdown).
func (s *Scheduler) Run(ctx context.Context) error {
	s.bootTime = time.Now().UTC()
	if err := s.LoadDAGs(ctx); err != nil {
		return err
	}
	if err := s.Recover(ctx); err != nil {
		return err
	}
	s.log.Info("scheduler started", "tick", s.opts.Tick.String(), "dags", len(s.allDAGs()))
	t := time.NewTicker(s.opts.Tick)
	defer t.Stop()
	for {
		s.tickOnce(ctx)
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopping; waiting for in-flight tasks")
			s.inflight.Wait()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// WaitInflight blocks until all dispatched tasks have finished. Used by tests
// to drive the loop deterministically.
func (s *Scheduler) WaitInflight() { s.inflight.Wait() }

func (s *Scheduler) tickOnce(ctx context.Context) {
	now := time.Now().UTC()
	s.createDueRuns(ctx, now)
	s.processActiveRuns(ctx)
}

// createDueRuns creates the next scheduled run for each scheduled DAG that is
// due. M1 is catchup-free: it anchors on the latest scheduled run (or boot
// time) so it only ever creates the single next run, never a backfill storm.
func (s *Scheduler) createDueRuns(ctx context.Context, now time.Time) {
	for _, d := range s.allDAGs() {
		// d comes from the cache (allDAGs), so d.Tasks is the parsed task set; a
		// 0-task shell is never scheduled.
		if d.Paused || d.Schedule == "" || len(d.Tasks) == 0 {
			continue
		}
		_, sched, ok := s.cachedDAG(d.DagID)
		if !ok || sched == nil {
			continue
		}
		next := sched.Next(s.scheduleAnchor(ctx, d))
		if now.Before(next) {
			continue
		}
		active, err := s.store.CountActiveRuns(ctx, d.DagID)
		if err != nil {
			s.log.Error("count active runs", "dag", d.DagID, "err", err)
			continue
		}
		if active >= d.MaxActiveRuns {
			continue
		}
		run := &model.DagRun{
			RunID:       runID(d.DagID, next),
			DagID:       d.DagID,
			LogicalDate: next,
			State:       model.RunQueued,
			TriggerType: model.TriggerSchedule,
		}
		if err := s.store.CreateDagRun(ctx, run); err != nil {
			// ErrAlreadyExists: a run for this period already exists. ErrNotFound:
			// the DAG was soft-deleted concurrently (the CreateDagRun guard) — both benign.
			if !errors.Is(err, store.ErrAlreadyExists) && !errors.Is(err, store.ErrNotFound) {
				s.log.Error("create scheduled run", "dag", d.DagID, "err", err)
			}
			continue
		}
		s.log.Info("scheduled run created", "dag", d.DagID, "logical_date", next.Format(time.RFC3339))
	}
}

// scheduleAnchor returns the point from which the next scheduled run's logical
// date is computed (next = schedule.Next(anchor)). It is the latest existing
// scheduled run's logical date; if there are none, catchup DAGs anchor at
// start_date (so missed periods are backfilled), while non-catchup DAGs anchor
// at boot time (so only future periods run — no backfill). max_active_runs +
// one-run-per-tick throttle catchup so it never floods (see §20 catchup storm).
func (s *Scheduler) scheduleAnchor(ctx context.Context, d *model.DAG) time.Time {
	runs, err := s.store.ListDagRuns(ctx, d.DagID, 100) // ordered logical_date DESC
	if err == nil {
		for _, r := range runs {
			if r.TriggerType == model.TriggerSchedule {
				return r.LogicalDate
			}
		}
	}
	if d.Catchup {
		// Anchor just before start_date so Next() yields the first period at/after it.
		return d.StartDate.Add(-time.Second)
	}
	return s.bootTime
}

func (s *Scheduler) processActiveRuns(ctx context.Context) {
	var active []*model.DagRun
	for _, st := range []model.RunState{model.RunQueued, model.RunRunning} {
		rs, err := s.store.ListDagRunsByState(ctx, st)
		if err != nil {
			s.log.Error("list runs by state", "state", st, "err", err)
			continue
		}
		active = append(active, rs...)
	}
	for _, run := range active {
		if err := s.processRun(ctx, run); err != nil {
			s.log.Error("process run", "run", run.RunID, "err", err)
		}
	}
}

// processRun advances a single run: expands task instances, promotes the run to
// running, propagates failures to downstream tasks, dispatches ready tasks, and
// finalizes the run when every task is terminal.
func (s *Scheduler) processRun(ctx context.Context, run *model.DagRun) error {
	// Serialize against MarkTask/MarkRun so the finalize decision below reads a task
	// snapshot no manual mark can invalidate mid-flight (dispatched tasks run in
	// their own goroutines, so this only holds for the cheap synchronous body).
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	d, err := s.dagFor(ctx, run.DagID)
	if err != nil {
		return err
	}

	tis, err := s.store.ListTaskInstances(ctx, run.RunID)
	if err != nil {
		return err
	}
	if len(tis) == 0 {
		if err := s.expandRun(ctx, run, d); err != nil {
			return err
		}
		if tis, err = s.store.ListTaskInstances(ctx, run.RunID); err != nil {
			return err
		}
	}

	// A run with no task instances has nothing to execute — this happens if the
	// DAG's last task was deleted after the run was queued (the gates prevent
	// run creation for a 0-task DAG, but not a live edit afterwards). Such a run
	// must FAIL, not finalize as a phantom success (empty set => allTerminal).
	if len(tis) == 0 {
		fin := time.Now().UTC()
		started := run.StartedAt
		if started == nil {
			started = &fin
		}
		if err := s.store.UpdateDagRunState(ctx, run.RunID, model.RunFailed, started, &fin); err != nil {
			return err
		}
		s.log.Warn("run failed: dag has no tasks to run", "run", run.RunID, "dag", run.DagID)
		return nil
	}

	if run.State == model.RunQueued {
		now := time.Now().UTC()
		if err := s.store.UpdateDagRunState(ctx, run.RunID, model.RunRunning, &now, nil); err != nil {
			return err
		}
		run.State = model.RunRunning
		run.StartedAt = &now
	}

	tiByTask := make(map[string]*model.TaskInstance, len(tis))
	for _, ti := range tis {
		tiByTask[ti.TaskID] = ti
	}
	taskByID := make(map[string]model.Task, len(d.Tasks))
	for _, t := range d.Tasks {
		taskByID[t.ID] = t
	}

	// 1. Block propagation to a fixpoint: a scheduled task whose trigger rule can
	// no longer be satisfied by its dependencies becomes upstream_failed.
	for changed := true; changed; {
		changed = false
		for _, t := range d.Tasks {
			ti := tiByTask[t.ID]
			if ti == nil || ti.State != model.TaskScheduled {
				continue
			}
			if _, blocked := taskGate(t, tiByTask); blocked {
				now := time.Now().UTC()
				ti.State = model.TaskUpstreamFailed
				ti.FinishedAt = &now
				if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
					return err
				}
				changed = true
			}
		}
	}

	// 2. Requeue up_for_retry tasks whose retry delay has elapsed.
	now := time.Now().UTC()
	for _, t := range d.Tasks {
		ti := tiByTask[t.ID]
		if ti == nil || ti.State != model.TaskUpForRetry {
			continue
		}
		readyAt := now
		if ti.FinishedAt != nil {
			readyAt = ti.FinishedAt.Add(time.Duration(t.RetryDelay) * time.Second)
		}
		if now.Before(readyAt) {
			continue
		}
		ti.State = model.TaskScheduled
		ti.ExecutorRef = ""
		ti.StartedAt = nil
		ti.FinishedAt = nil
		if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
			return err
		}
	}

	// 3. Dispatch ready scheduled tasks, highest priority first, respecting pool
	// slots. A pool that is full simply defers its remaining tasks to a later tick.
	var ready []*model.TaskInstance
	for _, t := range d.Tasks {
		ti := tiByTask[t.ID]
		if ti == nil || ti.State != model.TaskScheduled {
			continue
		}
		if r, _ := taskGate(t, tiByTask); r {
			ready = append(ready, ti)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].TaskID < ready[j].TaskID
	})
	poolRemaining := map[string]int{}
	for _, ti := range ready {
		rem, ok := poolRemaining[ti.Pool]
		if !ok {
			used, err := s.store.CountRunningInPool(ctx, ti.Pool)
			if err != nil {
				return err
			}
			rem = s.poolSlots(ctx, ti.Pool) - used
		}
		if rem <= 0 {
			poolRemaining[ti.Pool] = rem
			continue
		}
		if err := s.dispatch(ctx, run, taskByID[ti.TaskID], ti); err != nil {
			return err
		}
		poolRemaining[ti.Pool] = rem - 1
	}

	// 4. Finalize the run if every task is terminal.
	tis, err = s.store.ListTaskInstances(ctx, run.RunID)
	if err != nil {
		return err
	}
	allTerminal, anyFailed, anyCancelled := true, false, false
	for _, ti := range tis {
		if !ti.State.IsTerminal() {
			allTerminal = false
		}
		switch ti.State {
		case model.TaskFailed, model.TaskUpstreamFailed:
			anyFailed = true
		case model.TaskCancelled:
			anyCancelled = true
		}
	}
	if allTerminal {
		// a leftover cancelled task (e.g. a partial per-task retry) must NOT finalize
		// as a clean success or trigger downstreams.
		final := model.RunSuccess
		if anyFailed {
			final = model.RunFailed
		} else if anyCancelled {
			final = model.RunCancelled
		}
		fin := time.Now().UTC()
		if err := s.store.UpdateDagRunState(ctx, run.RunID, final, run.StartedAt, &fin); err != nil {
			return err
		}
		s.log.Info("run finished", "run", run.RunID, "state", final)
		// A mark-driven re-finalize is a silent recorded-outcome override (like
		// MarkRun): don't re-alert a run the operator already saw finish. Downstream
		// DAG triggers still fire (workflow dependency, not an alert).
		if s.notifySuppress[run.RunID] {
			delete(s.notifySuppress, run.RunID)
		} else {
			s.notifyRun(d, run, final, fin, tis)
		}
		if final == model.RunSuccess {
			s.triggerDownstreams(ctx, run)
		}
		s.clearSLAKeys(run.RunID)
	} else {
		// still running: enforce the run's dagrun_timeout (hard) and SLA (soft).
		s.enforceDeadlines(ctx, d, run, tis)
	}
	return nil
}

// enforceDeadlines checks a still-running run against its dagrun_timeout (hard
// kill) and SLA thresholds (soft alert), all measured from run start. Alerts fire
// at most once per run/task (deduped in slaAlerted). Called under finalizeMu.
func (s *Scheduler) enforceDeadlines(ctx context.Context, d *model.DAG, run *model.DagRun, tis []*model.TaskInstance) {
	if run.StartedAt == nil {
		return // never left the queue — nothing to measure from yet
	}
	elapsed := time.Now().UTC().Sub(*run.StartedAt)
	if d.DagrunTimeout > 0 && elapsed >= time.Duration(d.DagrunTimeout)*time.Second {
		s.timeoutRun(ctx, d, run, tis, elapsed)
		return // run is now terminal
	}
	if d.SLA > 0 && elapsed >= time.Duration(d.SLA)*time.Second && !s.slaAlerted[run.RunID] {
		s.slaAlerted[run.RunID] = true
		s.log.Warn("run SLA missed", "run", run.RunID, "sla_sec", d.SLA, "elapsed", elapsed.String())
		s.notifyDeadline(d, run, "sla_miss", "", d.SLA, elapsed)
	}
	// task-level SLA: a still-pending task past its deadline (from run start).
	for _, t := range d.Tasks {
		if t.SLA <= 0 || elapsed < time.Duration(t.SLA)*time.Second {
			continue
		}
		key := run.RunID + "\x00" + t.ID
		if s.slaAlerted[key] {
			continue
		}
		ti := findTI(tis, t.ID)
		if ti == nil || ti.State.IsTerminal() {
			continue // not created yet, or already done — no miss
		}
		s.slaAlerted[key] = true
		s.log.Warn("task SLA missed", "run", run.RunID, "task", t.ID, "sla_sec", t.SLA)
		s.notifyDeadline(d, run, "task_sla_miss", t.ID, t.SLA, elapsed)
	}
}

// timeoutRun hard-fails a run past its dagrun_timeout: kill running tasks, mark
// every non-terminal task timed_out, set the run timed_out, and alert. Mirrors
// CancelRun's kill pattern; called under finalizeMu.
func (s *Scheduler) timeoutRun(ctx context.Context, d *model.DAG, run *model.DagRun, tis []*model.TaskInstance, elapsed time.Duration) {
	fin := time.Now().UTC()
	if err := s.store.UpdateDagRunState(ctx, run.RunID, model.RunTimedOut, run.StartedAt, &fin); err != nil {
		s.log.Error("timeout run", "run", run.RunID, "err", err)
		return
	}
	for _, ti := range tis {
		if ti.State.IsTerminal() {
			continue
		}
		if ti.ExecutorRef != "" {
			_ = s.exec.Cancel(ctx, ti.ExecutorRef)
		}
		ti.State = model.TaskTimedOut
		ti.FinishedAt = &fin
		// Guarded (unlike CancelRun's override): if the task's poll goroutine
		// finalized it to a real terminal state between the snapshot and here, that
		// genuine outcome wins — we don't relabel a just-succeeded task timed_out.
		if _, err := s.store.UpdateTaskInstanceGuarded(ctx, ti, ti.ExecutorRef); err != nil {
			s.log.Error("timeout task", "ti", ti.ID, "err", err)
		}
	}
	s.log.Warn("run timed out", "run", run.RunID, "dagrun_timeout_sec", d.DagrunTimeout, "elapsed", elapsed.String())
	after, _ := s.store.ListTaskInstances(ctx, run.RunID)
	runCopy := *run
	runCopy.State = model.RunTimedOut
	s.notifyRun(d, &runCopy, model.RunTimedOut, fin, after)
	s.clearSLAKeys(run.RunID)
}

// clearSLAKeys drops a run's SLA-dedup entries once it finalizes (bounded growth).
func (s *Scheduler) clearSLAKeys(runID string) {
	delete(s.slaAlerted, runID)
	prefix := runID + "\x00"
	for k := range s.slaAlerted {
		if strings.HasPrefix(k, prefix) {
			delete(s.slaAlerted, k)
		}
	}
}

// findTI returns the task instance for taskID, or nil.
func findTI(tis []*model.TaskInstance, taskID string) *model.TaskInstance {
	for _, ti := range tis {
		if ti.TaskID == taskID {
			return ti
		}
	}
	return nil
}

// triggerDownstreams creates runs for any DAG whose trigger_after upstreams have
// all succeeded for this run's logical date (see docs/ARCHITECTURE.md §7.1).
func (s *Scheduler) triggerDownstreams(ctx context.Context, upstream *model.DagRun) {
	downs, err := s.store.ListDownstreams(ctx, upstream.DagID)
	if err != nil {
		s.log.Error("list downstreams", "dag", upstream.DagID, "err", err)
		return
	}
	for _, dn := range downs {
		ups, err := s.store.ListUpstreams(ctx, dn)
		if err != nil {
			s.log.Error("list upstreams", "dag", dn, "err", err)
			continue
		}
		allOK := true
		for _, up := range ups {
			// A soft-deleted (or never-registered) upstream is a dangling
			// dependency: it is evicted from the cache and can never produce a new
			// success, so treat it as unsatisfied — the downstream stays blocked
			// (its trigger_after ref to the archived DAG is dangling, by design).
			// This also ignores an archived upstream's stale historical success.
			if _, _, ok := s.cachedDAG(up); !ok {
				allOK = false
				break
			}
			r, err := s.store.GetDagRunByLogicalDate(ctx, up, upstream.LogicalDate)
			if err != nil || r.State != model.RunSuccess {
				allOK = false
				break
			}
		}
		if !allOK {
			continue
		}
		// Only fire downstreams that are actively registered: a soft-deleted or
		// unregistered downstream is not in the cache, and a 0-task shell is skipped.
		dd, _, ok := s.cachedDAG(dn)
		if !ok || len(dd.Tasks) == 0 {
			continue
		}
		maxActive := dd.MaxActiveRuns
		if active, _ := s.store.CountActiveRuns(ctx, dn); active >= maxActive {
			continue
		}
		nr := &model.DagRun{
			RunID:       runID(dn, upstream.LogicalDate),
			DagID:       dn,
			LogicalDate: upstream.LogicalDate,
			State:       model.RunQueued,
			TriggerType: model.TriggerDependency,
		}
		if err := s.store.CreateDagRun(ctx, nr); err != nil {
			if !errors.Is(err, store.ErrAlreadyExists) && !errors.Is(err, store.ErrNotFound) {
				s.log.Error("create dependency run", "dag", dn, "err", err)
			}
			continue
		}
		s.log.Info("dependency run created", "dag", dn, "upstream", upstream.DagID,
			"logical_date", upstream.LogicalDate.Format(time.RFC3339))
	}
}

func (s *Scheduler) dagFor(ctx context.Context, dagID string) (*model.DAG, error) {
	if d, _, ok := s.cachedDAG(dagID); ok {
		return d, nil
	}
	sd, err := s.store.GetDAG(ctx, dagID)
	if err != nil {
		return nil, err
	}
	return parser.Parse([]byte(sd.DefinitionYAML))
}

func (s *Scheduler) expandRun(ctx context.Context, run *model.DagRun, d *model.DAG) error {
	for _, t := range d.Tasks {
		ti := &model.TaskInstance{
			RunID:      run.RunID,
			TaskID:     t.ID,
			State:      model.TaskScheduled,
			MaxRetries: t.Retries,
			Pool:       t.Pool,
			Priority:   t.Priority,
			LogPath:    s.logPath(d.DagID, run.RunID, t.ID),
		}
		if err := s.store.CreateTaskInstance(ctx, ti); err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			return err
		}
	}
	return nil
}

// dispatch moves a task scheduled -> queued (synchronously, before launching,
// to prevent re-dispatch on the next tick), assigns this attempt's unique ref,
// and launches it in a goroutine. The ref embeds the try number so each retry
// re-runs on the executor (whose Launch is idempotent per ref).
func (s *Scheduler) dispatch(ctx context.Context, run *model.DagRun, t model.Task, ti *model.TaskInstance) error {
	ti.TryNumber++
	ti.State = model.TaskQueued
	ti.ExecutorRef = attemptRef(run.RunID, t.ID, ti.TryNumber)
	if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
		return err
	}
	tiVal := *ti
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		s.runTask(ctx, run, t, tiVal)
	}()
	return nil
}

// runTask launches a task on the executor (using the ref dispatch assigned) and
// then awaits completion by polling. The executor owns the child process, so a
// scheduler shutdown (ctx cancel) just stops the poll; the task keeps running
// and recovery re-attaches to it on restart.
func (s *Scheduler) runTask(ctx context.Context, run *model.DagRun, t model.Task, ti model.TaskInstance) {
	base := templateVars(run, t, ti.TryNumber)
	spec := executor.Spec{
		TaskRunID: ti.ExecutorRef,
		Type:      t.Type,
		Command:   renderCommand(t.Command, s.templateResolver(ctx, base, run.Params)),
		Env:       taskEnv(base, run.Params),
		Timeout:   time.Duration(t.Timeout) * time.Second,
		LogPath:   ti.LogPath,
	}
	if s.taskCancelled(ctx, ti.ID) {
		return // a CancelRun landed before we launched — don't start the process
	}
	if _, err := s.exec.Launch(ctx, spec); err != nil {
		s.log.Error("launch task", "run", run.RunID, "task", t.ID, "err", err)
		now := time.Now().UTC()
		ti.StartedAt = &now
		s.recordFailure(ctx, &ti, "launch error")
		return
	}

	now := time.Now().UTC()
	ti.State = model.TaskRunning
	ti.StartedAt = &now
	// guarded write: if a CancelRun (row → cancelled) or retry (ref cleared) landed
	// between the pre-Launch check and here, the CAS fails — kill the process we
	// just launched and bail, rather than resurrecting a cancelled task.
	if applied, err := s.store.UpdateTaskInstanceGuarded(ctx, &ti, ti.ExecutorRef); err != nil {
		s.log.Error("mark running", "ti", ti.ID, "err", err)
		return
	} else if !applied {
		_ = s.exec.Cancel(ctx, ti.ExecutorRef) // the process exists now; kill it
		s.log.Info("task cancelled during launch", "ref", ti.ExecutorRef)
		return
	}
	s.awaitCompletion(ctx, &ti)
}

// awaitCompletion polls the executor until the task exits (or the executor
// loses it), then records the outcome. On ctx cancellation it returns without
// finalizing, leaving the task running for recovery to re-attach.
func (s *Scheduler) awaitCompletion(ctx context.Context, ti *model.TaskInstance) {
	poll := time.NewTicker(s.opts.PollInterval)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return // leave the task running; recovery re-attaches
		case <-poll.C:
		}
		st, err := s.exec.Probe(ctx, ti.ExecutorRef)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Error("probe task", "ref", ti.ExecutorRef, "err", err)
			continue
		}
		switch st.Phase {
		case executor.PhaseExited:
			s.finalizeTask(ctx, ti, st.ExitCode)
			return
		case executor.PhaseUnknown:
			s.log.Warn("task lost (executor has no record)", "ref", ti.ExecutorRef)
			s.finalizeTaskLost(ctx, ti)
			return
		case executor.PhaseRunning:
			// keep polling
		}
	}
}

// taskCancelled reports whether a CancelRun already marked this task cancelled —
// a cheap pre-Launch early-out (the guarded write is the authoritative defense).
func (s *Scheduler) taskCancelled(ctx context.Context, id int64) bool {
	cur, err := s.store.GetTaskInstance(ctx, id)
	return err == nil && cur.State == model.TaskCancelled
}

// finalizeWrite persists a finalized task outcome through the guarded CAS: it
// applies only if the row still has this attempt's ref and is non-terminal, so a
// concurrent CancelRun (row → cancelled) or retry (ref cleared) is never
// clobbered. ref is the goroutine's own attempt ref, captured before the update
// mutates ti.ExecutorRef... but finalize keeps the same ref, so ti.ExecutorRef is it.
func (s *Scheduler) finalizeWrite(ctx context.Context, ti *model.TaskInstance, what string) {
	applied, err := s.store.UpdateTaskInstanceGuarded(ctx, ti, ti.ExecutorRef)
	if err != nil {
		s.log.Error(what, "ti", ti.ID, "err", err)
		return
	}
	if !applied {
		s.log.Info("finalize skipped (cancelled or retried concurrently)", "ti", ti.ID, "state", ti.State)
	}
}

func (s *Scheduler) finalizeTask(ctx context.Context, ti *model.TaskInstance, exitCode int) {
	if exitCode == 0 {
		now := time.Now().UTC()
		ti.State = model.TaskSuccess
		ti.FinishedAt = &now
		s.finalizeWrite(ctx, ti, "finalize task")
		s.log.Info("task finished", "ref", ti.ExecutorRef, "state", model.TaskSuccess, "exit", 0)
		return
	}
	s.recordFailure(ctx, ti, fmt.Sprintf("exit %d", exitCode))
}

// finalizeTaskLost handles a task whose executor record vanished (e.g. the
// executor process restarted) — treated as a failure (retry-aware).
func (s *Scheduler) finalizeTaskLost(ctx context.Context, ti *model.TaskInstance) {
	s.recordFailure(ctx, ti, "executor lost")
}

// recordFailure routes a failed attempt to up_for_retry if retries remain, else
// to failed. TryNumber counts attempts made (incremented at dispatch), so the
// task gets MaxRetries+1 total attempts.
func (s *Scheduler) recordFailure(ctx context.Context, ti *model.TaskInstance, reason string) {
	now := time.Now().UTC()
	ti.FinishedAt = &now
	if ti.TryNumber <= ti.MaxRetries {
		ti.State = model.TaskUpForRetry
		s.log.Info("task up for retry", "ref", ti.ExecutorRef, "try", ti.TryNumber, "max", ti.MaxRetries, "reason", reason)
	} else {
		ti.State = model.TaskFailed
		s.log.Info("task failed", "ref", ti.ExecutorRef, "try", ti.TryNumber, "reason", reason)
	}
	s.finalizeWrite(ctx, ti, "record failure")
}

// reattach spawns a poll goroutine for a task already running on the executor.
func (s *Scheduler) reattach(ctx context.Context, ti *model.TaskInstance) {
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		s.awaitCompletion(ctx, ti)
	}()
}

// Recover reconciles in-flight task instances with the executor after a
// scheduler restart (see docs/ARCHITECTURE.md §9). Tasks the executor is still
// running are re-attached; finished tasks are finalized; tasks the executor
// never started (queued but not launched) are reset to scheduled.
func (s *Scheduler) Recover(ctx context.Context) error {
	reattached, finalized, reset := 0, 0, 0

	queued, err := s.store.ListTaskInstancesByState(ctx, model.TaskQueued)
	if err != nil {
		return err
	}
	for _, ti := range queued {
		ref := ti.ExecutorRef // assigned at dispatch, before the queued-write
		st, err := s.exec.Probe(ctx, ref)
		if err != nil {
			s.log.Error("recover probe", "ref", ref, "err", err)
			continue
		}
		switch st.Phase {
		case executor.PhaseExited:
			s.finalizeTask(ctx, ti, st.ExitCode)
			finalized++
		case executor.PhaseRunning:
			ti.State = model.TaskRunning
			if ti.StartedAt == nil {
				now := time.Now().UTC()
				ti.StartedAt = &now
			}
			if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
				s.log.Error("recover mark running", "ti", ti.ID, "err", err)
				continue
			}
			s.reattach(ctx, ti)
			reattached++
		case executor.PhaseUnknown:
			// Never launched (crashed between queued-write and Launch): undo this
			// attempt's try increment and re-run it.
			ti.State = model.TaskScheduled
			ti.ExecutorRef = ""
			if ti.TryNumber > 0 {
				ti.TryNumber--
			}
			if err := s.store.UpdateTaskInstance(ctx, ti); err != nil {
				s.log.Error("recover reset queued", "ti", ti.ID, "err", err)
				continue
			}
			reset++
		}
	}

	running, err := s.store.ListTaskInstancesByState(ctx, model.TaskRunning)
	if err != nil {
		return err
	}
	for _, ti := range running {
		ref := ti.ExecutorRef
		st, err := s.exec.Probe(ctx, ref)
		if err != nil {
			s.log.Error("recover probe", "ref", ref, "err", err)
			continue
		}
		switch st.Phase {
		case executor.PhaseExited:
			ti.ExecutorRef = ref
			s.finalizeTask(ctx, ti, st.ExitCode)
			finalized++
		case executor.PhaseRunning:
			ti.ExecutorRef = ref
			s.reattach(ctx, ti)
			reattached++
		case executor.PhaseUnknown:
			s.log.Warn("recover: running task lost", "ref", ref)
			s.finalizeTaskLost(ctx, ti)
			finalized++
		}
	}

	if reattached+finalized+reset > 0 {
		s.log.Info("recovery complete", "reattached", reattached, "finalized", finalized, "reset", reset)
	}
	return nil
}

// attemptRef is the executor ref / idempotency key for one task attempt. The
// try number makes each retry a distinct ref so the executor re-runs it.
func attemptRef(runID, taskID string, try int) string {
	return fmt.Sprintf("%s/%s/%d", runID, taskID, try)
}

// templateVars are the substitution variables available to a task — both as
// {{ name }} placeholders in the command and as CRONOVA_<NAME> env vars. The
// logical date is what makes catchup meaningful: a backfilled run processes the
// data for the period it represents, not wall-clock "now".
func templateVars(run *model.DagRun, t model.Task, try int) map[string]string {
	return map[string]string{
		"run_id":           run.RunID,
		"dag_id":           run.DagID,
		"task_id":          t.ID,
		"try_number":       strconv.Itoa(try),
		"logical_date":     run.LogicalDate.Format("2006-01-02"),
		"logical_datetime": run.LogicalDate.Format(time.RFC3339),
	}
}

// taskEnv builds the process environment: the base vars as CRONOVA_<NAME> and
// each trigger param as CRONOVA_PARAM_<KEY>. Variables/connections are NOT
// blanket-injected — they enter only through explicit {{ var.X }}/{{ conn.Y.Z }}
// references in the command, so secrets don't leak into every task's env.
func taskEnv(base, params map[string]string) map[string]string {
	env := make(map[string]string, len(base)+len(params))
	for k, v := range base {
		env["CRONOVA_"+strings.ToUpper(k)] = v
	}
	for k, v := range params {
		env["CRONOVA_PARAM_"+strings.ToUpper(k)] = v
	}
	return env
}

// dotted names too (var.X, conn.ID.field, params.KEY), but never partial words.
var templateRe = regexp.MustCompile(`\{\{\s*([\w.]+)\s*\}\}`)

// renderCommand substitutes {{ name }} placeholders via resolve. Unknown names
// are left as-is so unrelated shell braces are not mangled.
func renderCommand(cmd string, resolve func(string) (string, bool)) string {
	return templateRe.ReplaceAllStringFunc(cmd, func(m string) string {
		key := templateRe.FindStringSubmatch(m)[1]
		if v, ok := resolve(key); ok {
			return v
		}
		return m
	})
}

// templateResolver resolves a placeholder name to its value: base vars first,
// then params.KEY, then var.KEY and conn.ID.field which hit the store lazily
// (only referenced values are fetched, and only referenced secrets are exposed).
func (s *Scheduler) templateResolver(ctx context.Context, base, params map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if v, ok := base[key]; ok {
			return v, true
		}
		if name, ok := strings.CutPrefix(key, "params."); ok {
			v, ok := params[name]
			return v, ok
		}
		if name, ok := strings.CutPrefix(key, "var."); ok {
			if vr, err := s.store.GetVariable(ctx, name); err == nil {
				return vr.Value, true
			}
			return "", false
		}
		if rest, ok := strings.CutPrefix(key, "conn."); ok {
			id, field, ok := strings.Cut(rest, ".")
			if !ok {
				return "", false
			}
			if c, err := s.store.GetConnection(ctx, id); err == nil {
				return connField(c, field)
			}
			return "", false
		}
		return "", false
	}
}

// connField returns a named field of a connection ({{ conn.ID.field }}). Extra
// JSON fields are reachable as extra.KEY. Unknown fields resolve to not-found
// (the placeholder is left intact) rather than an empty string, so a typo is visible.
func connField(c *model.Connection, field string) (string, bool) {
	switch field {
	case "host":
		return c.Host, true
	case "port":
		return strconv.Itoa(c.Port), true
	case "login", "user":
		return c.Login, true
	case "password":
		return c.Password, true
	case "type":
		return c.Type, true
	}
	if name, ok := strings.CutPrefix(field, "extra."); ok && c.Extra != "" {
		// RawMessage per key so one non-string value doesn't poison the whole
		// object; a JSON string decodes to its text, other scalars use raw text.
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(c.Extra), &m) == nil {
			raw, ok := m[name]
			if !ok {
				return "", false
			}
			var s string
			if json.Unmarshal(raw, &s) == nil {
				return s, true // JSON string → decoded value
			}
			return strings.TrimSpace(string(raw)), true // number/bool/etc → literal text
		}
	}
	return "", false
}

func (s *Scheduler) logPath(dagID, runID, taskID string) string {
	return filepath.Join(s.opts.LogDir, dagID, sanitize(runID), taskID+".log")
}

// --- pure helpers ---

// taskGate evaluates a task's trigger rule against its dependencies' current
// states, returning whether it is ready to dispatch and/or blocked.
func taskGate(t model.Task, byTask map[string]*model.TaskInstance) (ready, blocked bool) {
	states := make([]model.TaskState, 0, len(t.Deps))
	for _, dep := range t.Deps {
		if dti := byTask[dep]; dti != nil {
			states = append(states, dti.State)
		} else {
			states = append(states, "") // not yet expanded -> pending
		}
	}
	return model.EvalTriggerRule(t.TriggerRule, states)
}

func runID(dagID string, logical time.Time) string {
	// The .999999999 fraction is trimmed when zero, so whole-second logical
	// dates (cron, @every Ns) get clean ids, while sub-second schedules still
	// produce a unique run_id per logical_date (avoiding a PK collision).
	return fmt.Sprintf("%s__%s", dagID, logical.UTC().Format("20060102T150405.999999999Z"))
}

func sanitize(s string) string { return strings.ReplaceAll(s, ":", "_") }
