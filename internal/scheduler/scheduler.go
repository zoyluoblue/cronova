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
	"crypto/sha256"
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
	"github.com/zoyluo/cronova/internal/operator"
	"github.com/zoyluo/cronova/internal/scheduler/parser"
	"github.com/zoyluo/cronova/internal/store"
)

// Options configures a Scheduler.
type Options struct {
	DagDir       string        // directory of *.yaml DAG definitions ("" = none)
	LogDir       string        // root directory for per-task log files
	ProjectsDir  string        // directory of uploaded project dirs ("" = project attach disabled)
	WorkspaceDir string        // per-attempt project workspaces ("" = <tmp>/cronova-workspaces)
	Tick         time.Duration // scheduling loop interval
	PollInterval time.Duration // how often a running task is Probed for completion
	// Retention prunes finished runs (DB rows + their log directories) older
	// than this age. 0 disables pruning entirely — nothing is ever deleted.
	Retention time.Duration
	// AuditRetention is independent from run retention. 0 keeps audit entries forever.
	AuditRetention time.Duration
	Logger         *slog.Logger
	// Manual trigger admission bounds protect the metadata DB and scheduler from
	// an accidental or hostile trigger flood. Non-positive values use defaults.
	MaxManualQueuePerDAG int
	MaxManualQueueGlobal int
	// MaxQueuedRunsGlobal caps every queued run source, including schedules,
	// dependencies, and backfills. MaxConcurrentTasks caps executor occupancy.
	MaxQueuedRunsGlobal int
	MaxActiveRunsGlobal int
	MaxConcurrentTasks  int
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
	opBinary     string       // path to this binary, used to run typed operators (`<opBinary> run-op ...`)

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
	if opts.MaxManualQueuePerDAG <= 0 {
		opts.MaxManualQueuePerDAG = 1000
	}
	if opts.MaxManualQueueGlobal <= 0 {
		opts.MaxManualQueueGlobal = 10000
	}
	if opts.MaxQueuedRunsGlobal <= 0 {
		opts.MaxQueuedRunsGlobal = opts.MaxManualQueueGlobal
	}
	if opts.MaxActiveRunsGlobal <= 0 {
		opts.MaxActiveRunsGlobal = 1000
	}
	if opts.MaxConcurrentTasks <= 0 {
		opts.MaxConcurrentTasks = 64
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	opBinary, _ := os.Executable() // for `run-op` typed operators; falls back to "cronova" on PATH
	if opBinary == "" {
		opBinary = "cronova"
	}
	return &Scheduler{
		store:          st,
		exec:           ex,
		opts:           opts,
		log:            opts.Logger,
		dags:           map[string]*model.DAG{},
		schedules:      map[string]cron.Schedule{},
		notifyClient:   newNotifyClient(opts.AllowPrivateNotifyTargets),
		opBinary:       opBinary,
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
	// Reconcile the operational `paused` flag from the store, UNDER the same lock as
	// the cache publish. UpsertDAG preserves the existing paused column (it is not
	// part of the YAML) but never writes it back into d, and the parser always
	// leaves d.Paused=false — so without this read-back a restart (LoadDAGs) or an
	// editor save would cache Paused=false and silently resume a store-paused DAG.
	// Doing the read + publish atomically also serializes with SetPaused's locked
	// swap, so a pause that lands concurrently is never clobbered by a stale publish.
	if sd, err := s.store.GetDAG(ctx, d.DagID); err == nil {
		d.Paused = sd.Paused
	}
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

// SetPaused toggles a DAG's scheduling. It persists the change (source of truth)
// AND refreshes the live cache, so the very next tick honors it — the API used to
// write only the store row, leaving createDueRuns reading a stale cached
// d.Paused=false and scheduling a "paused" DAG until the next reload.
//
// The cache is refreshed by SWAPPING in a shallow copy rather than mutating the
// existing *model.DAG in place: createDueRuns reads d.Paused from a pointer it
// snapshotted outside s.mu, so an in-place write would be a data race. The old
// pointer stays immutable; the swap is race-free.
func (s *Scheduler) SetPaused(ctx context.Context, dagID string, paused bool) error {
	if err := s.store.SetDAGPaused(ctx, dagID, paused); err != nil {
		return err // ErrNotFound if the DAG is absent or soft-deleted
	}
	s.mu.Lock()
	if d, ok := s.dags[dagID]; ok {
		cp := *d // shallow copy; the shared Tasks slice is never mutated in place
		cp.Paused = paused
		s.dags[dagID] = &cp
	}
	s.mu.Unlock()
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
	if p.Slots < 1 {
		return 1
	}
	if p.Slots > model.MaxPoolSlots {
		return model.MaxPoolSlots
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
	snapshotRun(run, d)
	if err := s.store.CreateManualDagRunBounded(ctx, run, s.opts.MaxManualQueuePerDAG, s.opts.MaxManualQueueGlobal); err != nil {
		if errors.Is(err, model.ErrQueueFull) {
			return "", fmt.Errorf("dag %q reached a manual queue limit (per-DAG %d, global %d): %w", dagID, s.opts.MaxManualQueuePerDAG, s.opts.MaxManualQueueGlobal, err)
		}
		return "", err
	}
	s.log.Info("manual run created", "dag", dagID, "run", run.RunID)
	return run.RunID, nil
}

// maxBackfillRuns bounds one backfill request; a wider window than this is
// rejected so a typo'd date range cannot enqueue years of runs.
const maxBackfillRuns = 500

// Backfill creates one queued run per schedule point in [from, to], skipping
// periods that already have a run (any state). `to` is clamped to now — a
// backfill re-runs history; future periods belong to the scheduler. Execution
// is throttled by the DAG's max_active_runs exactly like catchup, so enqueuing
// hundreds of periods will not stampede.
func (s *Scheduler) Backfill(ctx context.Context, dagID string, from, to time.Time) (created, skipped int, err error) {
	d, _, ok := s.cachedDAG(dagID)
	if !ok {
		return 0, 0, fmt.Errorf("dag %q not found or not active: %w", dagID, store.ErrNotFound)
	}
	if len(d.Tasks) == 0 {
		return 0, 0, fmt.Errorf("dag %q: %w; add a task before backfilling", dagID, model.ErrNoTasks)
	}
	if d.Schedule == "" {
		return 0, 0, fmt.Errorf("dag %q has no schedule — backfill enumerates schedule periods", dagID)
	}
	sched, err := parser.ParseSchedule(d.Schedule)
	if err != nil {
		return 0, 0, fmt.Errorf("dag %q: bad schedule: %w", dagID, err)
	}
	now := time.Now().UTC()
	if to.After(now) {
		to = now
	}
	if !from.Before(to) {
		return 0, 0, fmt.Errorf("backfill window is empty: from %s is not before to %s", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	// Enumerate the whole window FIRST, before touching the store: an over-limit
	// window is rejected with zero side effects (previously the cap fired after
	// 500 runs were already committed, which the error then denied).
	var periods []time.Time
	for t := sched.Next(from.Add(-time.Second)); !t.IsZero() && !t.After(to); t = sched.Next(t) {
		if len(periods) >= maxBackfillRuns {
			return 0, 0, fmt.Errorf("window contains more than %d periods — narrow the range", maxBackfillRuns)
		}
		periods = append(periods, t)
	}
	for _, t := range periods {
		run := &model.DagRun{
			RunID:       fmt.Sprintf("%s__backfill_%d", dagID, t.Unix()),
			DagID:       dagID,
			LogicalDate: t,
			State:       model.RunQueued,
			TriggerType: model.TriggerBackfill,
		}
		snapshotRun(run, d)
		switch err := s.store.CreateDagRunBounded(ctx, run, s.opts.MaxQueuedRunsGlobal); {
		case errors.Is(err, store.ErrAlreadyExists):
			skipped++ // that period already ran (or is queued) — never double-run
		case errors.Is(err, model.ErrQueueFull):
			return created, skipped, fmt.Errorf("global queued-run limit %d reached: %w", s.opts.MaxQueuedRunsGlobal, err)
		case err != nil:
			return created, skipped, err
		default:
			created++
		}
	}
	s.log.Info("backfill enqueued", "dag", dagID, "created", created, "skipped", skipped,
		"from", from.Format(time.RFC3339), "to", to.Format(time.RFC3339))
	return created, skipped, nil
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
	if err := s.prepareRetryDefinition(ctx, run, d, tiByTask, ids); err != nil {
		return err
	}
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
	if err := s.prepareRetryDefinition(ctx, run, d, tiByTask, ids); err != nil {
		return err
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
	if target == model.RunSuccess {
		if err := s.store.UpdateDagRunSuccess(ctx, runID, run.StartedAt, fin); err != nil {
			return err
		}
		// Preserve MarkRun's synchronous trigger behavior when capacity is
		// available. A full queue leaves the durable event for a later tick.
		s.processPendingDependencyEvents(ctx)
	} else if err := s.store.UpdateDagRunState(ctx, runID, target, run.StartedAt, fin); err != nil {
		return err
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
	d, err := s.dagForRun(ctx, run)
	if err != nil {
		return nil, nil, nil, err
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

// prepareRetryDefinition is the explicit version boundary: a finished run keeps
// its original snapshot forever unless an operator retries it. The retry adopts
// the latest DAG, creates any newly-added tasks, and stamps reset tasks with the
// new definition hash. Removed task instances remain as history but are ignored
// by finalization because they are not part of the adopted snapshot.
func (s *Scheduler) prepareRetryDefinition(ctx context.Context, run *model.DagRun, d *model.DAG, tiByTask map[string]*model.TaskInstance, reset map[string]bool) error {
	hash := definitionHash(d.DefinitionYAML)
	if err := s.store.UpdateDagRunDefinition(ctx, run.RunID, d.DefinitionYAML, hash); err != nil {
		return err
	}
	run.DefinitionYAML = d.DefinitionYAML
	run.DefinitionHash = hash
	for _, t := range d.Tasks {
		ti := tiByTask[t.ID]
		if ti == nil {
			ti = &model.TaskInstance{
				RunID: run.RunID, TaskID: t.ID, State: model.TaskScheduled,
				MaxRetries: t.Retries, Pool: t.Pool, Priority: t.Priority,
				DefinitionHash: hash, LogPath: s.logPath(d.DagID, run.RunID, t.ID),
			}
			if err := s.store.CreateTaskInstance(ctx, ti); err != nil {
				return err
			}
			tiByTask[t.ID] = ti
			continue
		}
		if reset[t.ID] {
			ti.MaxRetries = t.Retries
			ti.Pool = t.Pool
			ti.Priority = t.Priority
			ti.DefinitionHash = hash
		}
	}
	return nil
}

// retryDelay computes the wait before a task's next attempt. tries is the
// number of attempts already made (TryNumber after a failure), so the first
// retry of an exponential task waits the base delay, the second 2×, then 4×…
// capped by retry_delay_max when set. Fixed (the default) always waits the base.
func retryDelay(t model.Task, tries int) time.Duration {
	base := time.Duration(t.RetryDelay) * time.Second
	if t.RetryBackoff != model.BackoffExponential || base <= 0 {
		return base
	}
	shift := tries - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 20 { // 2^20 × base: far past any real cap
		shift = 20
	}
	d := base << shift
	if d < base { // shift overflowed int64 (a huge retry_delay): saturate, don't go negative
		d = time.Duration(1<<62 - 1)
	}
	if t.RetryDelayMax > 0 {
		if max := time.Duration(t.RetryDelayMax) * time.Second; d > max {
			return max
		}
	}
	// No cap configured and the growth exceeded any sane wait: clamp to 24h so
	// an overflow/extreme config can never turn into a negative (=hot) retry loop.
	if const24h := 24 * time.Hour; d > const24h && t.RetryDelayMax <= 0 {
		return const24h
	}
	return d
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
	// Sweep orphaned project workspaces now (recovery has claimed the live ones,
	// and dispatch hasn't started, so no age guard is needed)…
	s.gcWorkspaces(ctx, 0)
	s.housekeeping(ctx)
	s.log.Info("scheduler started", "tick", s.opts.Tick.String(), "dags", len(s.allDAGs()))
	t := time.NewTicker(s.opts.Tick)
	defer t.Stop()
	// …and periodically, for workspaces of recovered tasks that finalize outside
	// runTask (whose defer would otherwise have cleaned them).
	gc := time.NewTicker(30 * time.Minute)
	defer gc.Stop()
	for {
		s.tickOnce(ctx)
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopping; waiting for in-flight tasks")
			s.inflight.Wait()
			return ctx.Err()
		case <-gc.C:
			s.gcWorkspaces(ctx, time.Hour) // age guard: skip anything mid-launch
			s.housekeeping(ctx)
		case <-t.C:
		}
	}
}

func (s *Scheduler) housekeeping(ctx context.Context) {
	if err := s.store.DeleteExpiredSessions(ctx); err != nil {
		s.log.Error("expired session cleanup failed", "err", err)
	}
	if s.opts.AuditRetention > 0 {
		cutoff := time.Now().UTC().Add(-s.opts.AuditRetention)
		if n, err := s.store.PruneAudit(ctx, cutoff); err != nil {
			s.log.Error("audit retention cleanup failed", "err", err)
		} else if n > 0 {
			s.log.Info("audit retention pruned entries", "entries", n, "older_than", s.opts.AuditRetention.String())
		}
	}
	s.pruneOldRuns(ctx)
}

// WaitInflight blocks until all dispatched tasks have finished. Used by tests
// to drive the loop deterministically.
func (s *Scheduler) WaitInflight() { s.inflight.Wait() }

// pruneOldRuns deletes finished runs older than the retention window — DB rows
// first (transactional), then each run's log directory. A missing log dir is
// fine; a failed dir removal is only logged, and the next sweep retries it.
// No-op when Retention is 0 (disabled).
func (s *Scheduler) pruneOldRuns(ctx context.Context) {
	if s.opts.Retention <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.opts.Retention)
	pruned, err := s.store.PruneRuns(ctx, cutoff)
	if err != nil {
		s.log.Error("retention prune failed", "err", err)
		return
	}
	if len(pruned) == 0 {
		return
	}
	var logErrs int
	for _, r := range pruned {
		if err := os.RemoveAll(RunLogDir(s.opts.LogDir, r.DagID, r.RunID)); err != nil {
			logErrs++
		}
	}
	s.log.Info("retention pruned finished runs",
		"runs", len(pruned), "older_than", s.opts.Retention.String(), "log_dir_errors", logErrs)
}

func (s *Scheduler) tickOnce(ctx context.Context) {
	now := time.Now().UTC()
	// Give previously-deferred dependency runs first access to newly-available
	// queue capacity, then pick up events published by runs finalized this tick.
	s.processPendingDependencyEvents(ctx)
	s.createDueRuns(ctx, now)
	s.processActiveRuns(ctx)
	s.processPendingDependencyEvents(ctx)
}

// createDueRuns creates the next scheduled run for each scheduled DAG that is
// due. M1 is catchup-free: it anchors on the latest scheduled run (or boot
// time) so it only ever creates the single next run, never a backfill storm.
func (s *Scheduler) createDueRuns(ctx context.Context, now time.Time) {
	for _, d := range s.allDAGs() {
		// d comes from the cache (allDAGs), so d.Tasks is the parsed task set; a
		// 0-task shell is never scheduled.
		if d.Schedule == "" || len(d.Tasks) == 0 {
			continue
		}
		// Re-read the DAG under the lock and gate on THAT Paused, not the pointer
		// snapshotted by allDAGs(): SetPaused swaps in a fresh pointer, so a pause
		// that landed after the snapshot is still honored on this same tick.
		cd, sched, ok := s.cachedDAG(d.DagID)
		if !ok || sched == nil || cd.Paused {
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
		snapshotRun(run, cd)
		if err := s.store.CreateDagRunBounded(ctx, run, s.opts.MaxQueuedRunsGlobal); err != nil {
			// ErrAlreadyExists: a run for this period already exists. ErrNotFound:
			// the DAG was soft-deleted concurrently (the CreateDagRun guard) — both benign.
			if errors.Is(err, model.ErrQueueFull) {
				s.log.Warn("global queued-run limit reached", "limit", s.opts.MaxQueuedRunsGlobal)
			} else if !errors.Is(err, store.ErrAlreadyExists) && !errors.Is(err, store.ErrNotFound) {
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
	// A dedicated latest-scheduled-run lookup, NOT a windowed listing: a burst
	// of manual/backfill runs must never crowd the anchor out of view, or a
	// catchup DAG would fall back to start_date and replay history for real.
	if r, err := s.store.LatestScheduledRun(ctx, d.DagID); err == nil {
		return r.LogicalDate
	}
	if d.Catchup {
		// Anchor just before start_date so Next() yields the first period at/after it.
		return d.StartDate.Add(-time.Second)
	}
	return s.bootTime
}

func (s *Scheduler) processActiveRuns(ctx context.Context) {
	queued, err := s.store.ListDagRunsByState(ctx, model.RunQueued)
	if err != nil {
		s.log.Error("list runs by state", "state", model.RunQueued, "err", err)
	}
	running, err := s.store.ListDagRunsByState(ctx, model.RunRunning)
	if err != nil {
		s.log.Error("list runs by state", "state", model.RunRunning, "err", err)
	}
	for _, run := range running {
		if err := s.processRun(ctx, run); err != nil {
			s.log.Error("process run", "run", run.RunID, "err", err)
		}
	}
	// Gate queued→running on max_active_runs. This is what makes a 500-period
	// backfill (or a burst of manual triggers) execute max_active_runs at a
	// time instead of stampeding — the run-level mutual exclusion the docs
	// promise. Runs are listed logical_date ASC, so older periods start first;
	// a run past its dagrun_timeout is still processed so it can time out.
	runningBy := map[string]int{}
	for _, r := range running {
		runningBy[r.DagID]++
	}
	globalRunning := len(running)
	for _, run := range queued {
		if globalRunning >= s.opts.MaxActiveRunsGlobal {
			break
		}
		maxActive := 1
		if d, _, ok := s.cachedDAG(run.DagID); ok && d.MaxActiveRuns > 0 {
			maxActive = d.MaxActiveRuns
		}
		if runningBy[run.DagID] >= maxActive {
			continue // stays queued; picked up on a later tick as slots free
		}
		runningBy[run.DagID]++
		globalRunning++
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
	d, err := s.dagForRun(ctx, run)
	if err != nil {
		return err
	}

	tis, err := s.store.ListTaskInstances(ctx, run.RunID)
	if err != nil {
		return err
	}
	// Reconcile missing snapshot tasks only. This repairs a crash halfway through
	// first expansion without issuing one doomed UNIQUE insert per task per tick.
	if len(tis) < len(d.Tasks) {
		if err := s.expandRun(ctx, run, d, tis); err != nil {
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
			readyAt = ti.FinishedAt.Add(retryDelay(t, ti.TryNumber))
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
	activeTasks, err := s.store.CountActiveTaskInstances(ctx)
	if err != nil {
		return err
	}
	globalRemaining := s.opts.MaxConcurrentTasks - activeTasks
	for _, ti := range ready {
		if globalRemaining <= 0 {
			break
		}
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
		globalRemaining--
	}

	// 4. Finalize the run if every task is terminal.
	tis, err = s.store.ListTaskInstances(ctx, run.RunID)
	if err != nil {
		return err
	}
	finalTIByTask := make(map[string]*model.TaskInstance, len(tis))
	for _, ti := range tis {
		finalTIByTask[ti.TaskID] = ti
	}
	allTerminal, anyFailed, anyCancelled := true, false, false
	for _, task := range d.Tasks {
		ti := finalTIByTask[task.ID]
		if ti == nil {
			allTerminal = false
			continue
		}
		if !ti.State.IsTerminal() {
			allTerminal = false
		}
		switch ti.State {
		case model.TaskFailed, model.TaskUpstreamFailed, model.TaskTimedOut:
			anyFailed = true // a leftover timed_out task must fail the run, not pass as success
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
		if final == model.RunSuccess {
			// State and dependency publication share one transaction, so neither a
			// restart nor a queue-capacity window can lose the downstream trigger.
			if err := s.store.UpdateDagRunSuccess(ctx, run.RunID, run.StartedAt, &fin); err != nil {
				return err
			}
		} else if err := s.store.UpdateDagRunState(ctx, run.RunID, final, run.StartedAt, &fin); err != nil {
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

// processPendingDependencyEvents replays durable success signals oldest-first.
// An event is consumed only after every eligible downstream was either created
// or already existed. Queue admission failures remain pending for a later tick.
func (s *Scheduler) processPendingDependencyEvents(ctx context.Context) {
	events, err := s.store.ListPendingEvents(ctx, model.EventSourceDependency, 1000)
	if err != nil {
		s.log.Error("list dependency events", "err", err)
		return
	}
	for _, event := range events {
		upstream, err := s.store.GetDagRun(ctx, event.EventKey)
		if errors.Is(err, store.ErrNotFound) {
			if err := s.store.ConsumeEvent(ctx, event.ID); err != nil {
				s.log.Error("consume orphan dependency event", "event", event.ID, "err", err)
			}
			continue
		}
		if err != nil {
			s.log.Error("load dependency event run", "event", event.ID, "run", event.EventKey, "err", err)
			continue
		}
		if upstream.State != model.RunSuccess {
			// A later manual failed mark invalidates the pending success signal.
			// Marking the run successful again re-arms this same idempotency key.
			if upstream.State.IsTerminal() {
				if err := s.store.ConsumeEvent(ctx, event.ID); err != nil {
					s.log.Error("consume obsolete dependency event", "event", event.ID, "err", err)
				}
			}
			continue
		}
		deferred, err := s.triggerDownstreams(ctx, upstream)
		if err != nil {
			s.log.Error("deliver dependency event", "event", event.ID, "run", upstream.RunID, "err", err)
			continue
		}
		if deferred {
			s.log.Debug("dependency event deferred", "event", event.ID, "run", upstream.RunID,
				"queued_limit", s.opts.MaxQueuedRunsGlobal)
			continue
		}
		if err := s.store.ConsumeEvent(ctx, event.ID); err != nil {
			s.log.Error("consume dependency event", "event", event.ID, "run", upstream.RunID, "err", err)
		}
	}
}

// triggerDownstreams creates runs for any DAG whose trigger_after upstreams have
// all succeeded for this run's logical date (see docs/ARCHITECTURE.md §7.1).
// deferred reports that at least one eligible run could not enter the global
// queue; callers must retain the durable event and retry it later.
func (s *Scheduler) triggerDownstreams(ctx context.Context, upstream *model.DagRun) (deferred bool, err error) {
	downs, err := s.store.ListDownstreams(ctx, upstream.DagID)
	if err != nil {
		return false, fmt.Errorf("list downstreams for %q: %w", upstream.DagID, err)
	}
	for _, dn := range downs {
		ups, err := s.store.ListUpstreams(ctx, dn)
		if err != nil {
			return deferred, fmt.Errorf("list upstreams for %q: %w", dn, err)
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
			if errors.Is(err, store.ErrNotFound) || (err == nil && r.State != model.RunSuccess) {
				allOK = false
				break
			}
			if err != nil {
				return deferred, fmt.Errorf("load upstream %q for downstream %q: %w", up, dn, err)
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
		nr := &model.DagRun{
			RunID:       runID(dn, upstream.LogicalDate),
			DagID:       dn,
			LogicalDate: upstream.LogicalDate,
			State:       model.RunQueued,
			TriggerType: model.TriggerDependency,
		}
		snapshotRun(nr, dd)
		if err := s.store.CreateDagRunBounded(ctx, nr, s.opts.MaxQueuedRunsGlobal); err != nil {
			if errors.Is(err, model.ErrQueueFull) {
				deferred = true
				continue
			}
			if errors.Is(err, store.ErrAlreadyExists) || errors.Is(err, store.ErrNotFound) {
				continue
			}
			return deferred, fmt.Errorf("create dependency run for %q: %w", dn, err)
		}
		s.log.Info("dependency run created", "dag", dn, "upstream", upstream.DagID,
			"logical_date", upstream.LogicalDate.Format(time.RFC3339))
	}
	return deferred, nil
}

func definitionHash(yamlText string) string {
	sum := sha256.Sum256([]byte(yamlText))
	return fmt.Sprintf("%x", sum[:])
}

func snapshotRun(run *model.DagRun, d *model.DAG) {
	run.DefinitionYAML = d.DefinitionYAML
	run.DefinitionHash = definitionHash(d.DefinitionYAML)
}

func (s *Scheduler) dagForRun(ctx context.Context, run *model.DagRun) (*model.DAG, error) {
	if run.DefinitionYAML != "" {
		return parser.Parse([]byte(run.DefinitionYAML))
	}
	// Legacy queued/running rows predate snapshots. Capture exactly once before
	// expansion so they become deterministic from this point onward.
	d, _, ok := s.cachedDAG(run.DagID)
	if !ok {
		sd, err := s.store.GetDAG(ctx, run.DagID)
		if err != nil {
			return nil, err
		}
		d, err = parser.Parse([]byte(sd.DefinitionYAML))
		if err != nil {
			return nil, err
		}
	}
	snapshotRun(run, d)
	if err := s.store.UpdateDagRunDefinition(ctx, run.RunID, run.DefinitionYAML, run.DefinitionHash); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Scheduler) expandRun(ctx context.Context, run *model.DagRun, d *model.DAG, current []*model.TaskInstance) error {
	existing := make(map[string]bool, len(current))
	for _, ti := range current {
		existing[ti.TaskID] = true
	}
	for _, t := range d.Tasks {
		if existing[t.ID] {
			continue
		}
		ti := &model.TaskInstance{
			RunID:          run.RunID,
			TaskID:         t.ID,
			State:          model.TaskScheduled,
			MaxRetries:     t.Retries,
			Pool:           t.Pool,
			Priority:       t.Priority,
			DefinitionHash: run.DefinitionHash,
			LogPath:        s.logPath(d.DagID, run.RunID, t.ID),
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
	resolve := s.templateResolver(ctx, base, run.Params)
	env := taskEnv(base, run.Params)
	// secrets accumulates every connection-password value substituted into this
	// task, so we can mask them from anything echoed to the log (the "$ ..." line
	// and, for http tasks, the request URL). The command still runs with the real
	// values — only the log echo is redacted.
	var secrets []string
	command := renderCommand(t.Command, collectSecrets(resolve, &secrets))

	// Project attach: stage a fresh copy of the uploaded project and run the
	// command with cwd = that copy (so `python3 main.py` resolves). The workspace
	// is removed when this attempt finalizes; on ctx cancellation (shutdown /
	// recovery) it is kept so a re-attached task can still read it. Shell-only —
	// python/sql/http rewrite Command to an absolute-path `run-op`, where a cwd is
	// meaningless, so staging there would just copy files nothing reads.
	var workspace string
	keepWorkspace := false                                      // set when the process is launched but the row didn't flip to running
	if (t.Type == "shell" || t.Type == "") && t.Project != "" { // "" == shell (parser's default)
		ws, err := s.stageProject(t.Project, ti.ExecutorRef)
		if err != nil {
			s.log.Error("stage project", "run", run.RunID, "task", t.ID, "project", t.Project, "err", err)
			now := time.Now().UTC()
			ti.StartedAt = &now
			s.recordFailure(ctx, &ti, "stage project: "+err.Error())
			return
		}
		workspace = ws
		env["CRONOVA_PROJECT_DIR"] = ws
		// Remove the copy when this attempt finalizes — EXCEPT (a) on ctx
		// cancellation (shutdown/recovery re-attaches and re-reads it) or (b) when
		// the process launched but we couldn't mark the row running (keepWorkspace):
		// the task is live on the executor with its row still queued, so recovery
		// must find the workspace intact. Deleting it would pull the cwd out from
		// under a running process.
		defer func() {
			if ctx.Err() == nil && !keepWorkspace {
				_ = os.RemoveAll(workspace)
			}
		}()
	}

	// Typed operators (http/python/sql) run natively via `<binary> run-op <type>`,
	// reusing the executor's normal launch/probe/cancel/log path. The spec (templates
	// resolved) is passed by env, not interpolated into the shell string — no injection.
	switch t.Type {
	case "", "shell", "jar":
		// These task types execute their validated command directly.
	case "http":
		if t.HTTP == nil {
			now := time.Now().UTC()
			ti.StartedAt = &now
			s.recordFailure(ctx, &ti, "invalid http task definition")
			return
		}
		// The secrets substituted into url/headers/body are collected so the
		// executor masks them in run-op's echoed request line (child output).
		hspec := resolveHTTPSpec(*t.HTTP, collectSecrets(resolve, &secrets))
		blob, _ := json.Marshal(hspec)
		env["CRONOVA_OP_SPEC"] = string(blob)
		command = shellQuote(s.opBinary) + " run-op http"
	case "python":
		// `command` already holds the templated Python code (its secrets are in
		// `secrets`); the executor masks them in the interpreter's traceback output.
		blob, _ := json.Marshal(map[string]string{"code": command})
		env["CRONOVA_OP_SPEC"] = string(blob)
		command = shellQuote(s.opBinary) + " run-op python"
	case "sql":
		// `command` already holds the templated query; build driver+DSN from the conn.
		// The DSN password bypasses template resolution, so collect it explicitly so
		// a driver error that echoes the DSN is redacted from the log.
		blob, _ := json.Marshal(s.sqlOpSpec(ctx, t.Conn, command, &secrets))
		env["CRONOVA_OP_SPEC"] = string(blob)
		command = shellQuote(s.opBinary) + " run-op sql"
	default:
		now := time.Now().UTC()
		ti.StartedAt = &now
		s.recordFailure(ctx, &ti, "unsupported task type: "+t.Type)
		return
	}
	spec := executor.Spec{
		TaskRunID: ti.ExecutorRef,
		Type:      t.Type,
		Command:   command,
		Env:       env,
		Timeout:   time.Duration(t.Timeout) * time.Second,
		LogPath:   ti.LogPath,
		Dir:       workspace, // "" unless a project is attached
		Redact:    secrets,   // executor masks these in the echo AND child output
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
		keepWorkspace = true // process is live but row stayed queued; recovery re-attaches and needs the cwd
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
			// Guarded CAS (ref match + non-terminal), NOT a blind write: the console
			// is already live during recovery, so a CancelRun could have marked this
			// task terminal between our Probe and this write. A blind UPDATE would
			// resurrect the cancelled task; the CAS just fails and we skip reattach.
			applied, err := s.store.UpdateTaskInstanceGuarded(ctx, ti, ref)
			if err != nil {
				s.log.Error("recover mark running", "ti", ti.ID, "err", err)
				continue
			}
			if !applied {
				// A CancelRun made the row terminal while its process is still live on
				// the executor. Kill it (mirrors the launch-race path) so we don't leave
				// an orphan running under a cancelled task, then skip reattach.
				_ = s.exec.Cancel(ctx, ref)
				s.log.Info("recover: task cancelled concurrently, killed orphan", "ti", ti.ID, "ref", ref)
				continue
			}
			s.reattach(ctx, ti)
			reattached++
		case executor.PhaseUnknown:
			// Never launched (crashed between queued-write and Launch): undo this
			// attempt's try increment and re-run it. Guarded on the OLD ref so a
			// concurrent cancel/retry (which makes the row terminal or rewrites the
			// ref) wins instead of being clobbered by this reset.
			oldRef := ref
			ti.State = model.TaskScheduled
			ti.ExecutorRef = ""
			if ti.TryNumber > 0 {
				ti.TryNumber--
			}
			applied, err := s.store.UpdateTaskInstanceGuarded(ctx, ti, oldRef)
			if err != nil {
				s.log.Error("recover reset queued", "ti", ti.ID, "err", err)
				continue
			}
			if !applied {
				s.log.Info("recover: task changed concurrently, skip reset", "ti", ti.ID, "ref", oldRef)
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

// isSecretKey reports whether a template key names a value that must never be
// echoed to a task log — currently any connection password ({{ conn.ID.password }}
// or a nested {{ conn.ID.extra.password }}).
func isSecretKey(key string) bool {
	return strings.HasPrefix(key, "conn.") && strings.HasSuffix(key, ".password")
}

// collectSecrets wraps a resolver so that every secret value it hands back (see
// isSecretKey) is appended to *out. The caller uses *out to redact those exact
// substrings from anything it writes to the log, while the task still executes
// with the real resolved values.
func collectSecrets(resolve func(string) (string, bool), out *[]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := resolve(key)
		if ok && v != "" && isSecretKey(key) {
			*out = append(*out, v)
		}
		return v, ok
	}
}

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

// resolveHTTPSpec applies {{ }} templates to an http task's url, header values,
// and body (the same resolver used for shell commands — var./conn./params./etc).
func resolveHTTPSpec(h model.HTTPSpec, resolve func(string) (string, bool)) model.HTTPSpec {
	out := h
	out.URL = renderCommand(h.URL, resolve)
	out.Body = renderCommand(h.Body, resolve)
	if len(h.Headers) > 0 {
		hdr := make(map[string]string, len(h.Headers))
		for k, v := range h.Headers {
			hdr[k] = renderCommand(v, resolve)
		}
		out.Headers = hdr
	}
	return out
}

// shellQuote wraps s in single quotes for safe inclusion in an `sh -c` string
// (the binary path may contain spaces). Embedded single quotes are escaped.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sqlOpSpec resolves a sql task's connection into a driver+DSN. A resolution
// failure is carried in the spec's Err (run-op logs it and fails the task) rather
// than aborting dispatch, so the failure shows up in the task log like any other.
// The connection password is appended to *secrets (it is embedded in the DSN,
// bypassing template resolution) so the executor can redact it from any driver
// error the operator echoes.
func (s *Scheduler) sqlOpSpec(ctx context.Context, connID, query string, secrets *[]string) operator.SQLSpec {
	c, err := s.store.GetConnection(ctx, connID)
	if err != nil {
		return operator.SQLSpec{Query: query, Err: fmt.Sprintf("connection %q not found: %v", connID, err)}
	}
	if secrets != nil && c.Password != "" {
		*secrets = append(*secrets, c.Password)
	}
	driver, dsn, err := operator.BuildDSN(c)
	if err != nil {
		return operator.SQLSpec{Query: query, Err: err.Error()}
	}
	return operator.SQLSpec{Driver: driver, DSN: dsn, Query: query}
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
	return filepath.Join(RunLogDir(s.opts.LogDir, dagID, runID), taskID+".log")
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

// RunLogDir returns the directory holding a run's per-task log files — the
// same layout logPath writes to. Exported so operational tooling (`cronova
// prune`) deletes exactly what the scheduler wrote.
func RunLogDir(logDir, dagID, runID string) string {
	return filepath.Join(logDir, dagID, sanitize(runID))
}
