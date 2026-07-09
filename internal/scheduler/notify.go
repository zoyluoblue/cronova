package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"syscall"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// notifyPayload is the JSON body POSTed to a DAG's notify webhook. `text` is a
// human-readable summary so Slack/Feishu/Discord incoming webhooks render it
// directly; the structured fields serve custom endpoints. All times are UTC.
type notifyPayload struct {
	Text         string   `json:"text"`
	DagID        string   `json:"dag_id"`
	RunID        string   `json:"run_id"`
	State        string   `json:"state"`
	LogicalDate  string   `json:"logical_date"`
	StartedAt    string   `json:"started_at,omitempty"`
	FinishedAt   string   `json:"finished_at,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
	FailedTasks  []string `json:"failed_tasks,omitempty"`  // tasks that did not succeed (failed/upstream_failed/cancelled/timed_out)
	TaskID       string   `json:"task_id,omitempty"`       // set for a task-level SLA miss
	ThresholdSec int      `json:"threshold_sec,omitempty"` // the SLA/timeout deadline that was breached
	ElapsedMS    int64    `json:"elapsed_ms,omitempty"`    // how long the run had been going at breach time
}

// notifyTargetBlocked reports whether an outbound webhook must NOT connect to ip.
// It refuses every non-public range that could reach an internal service or the
// cloud metadata endpoint: loopback, RFC1918/ULA private, RFC6598 CGNAT, all
// link-local, all multicast, and unspecified. NAT64 (64:ff9b::/96) addresses are
// unwrapped to their embedded IPv4 first, so an IPv6-only/DNS64 path can't smuggle
// 169.254.169.254 past the guard as an ordinary global-unicast v6 address.
func notifyTargetBlocked(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := nat64Embedded(ip); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// RFC 6598 shared address space 100.64.0.0/10 (carrier-grade NAT; commonly
	// internal in cloud VPCs) — not covered by net.IP.IsPrivate().
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}

// nat64Embedded returns the IPv4 embedded in a 64:ff9b::/96 NAT64 address, or nil.
func nat64Embedded(ip net.IP) net.IP {
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil { // nil, or a plain/mapped IPv4 (not NAT64)
		return nil
	}
	prefix := []byte{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}
	for i, b := range prefix {
		if v6[i] != b {
			return nil
		}
	}
	return net.IPv4(v6[12], v6[13], v6[14], v6[15])
}

// newNotifyClient builds the HTTP client used for webhook delivery. It hardens
// against SSRF: redirects are never followed (a public URL can't 302-pivot into
// an internal service), and — unless explicitly allowed — connections to
// non-public IPs are refused at DIAL time (see notifyTargetBlocked), which also
// defeats DNS-rebinding since the check runs on the resolved address.
func newNotifyClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if notifyTargetBlocked(ip) {
				return fmt.Errorf("notify: refusing to connect to non-public address %q", host)
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
	}
}

// notifyRun fires the DAG's webhook (async, best-effort) when a finished run's
// state matches the DAG's notify_on list. It never blocks the scheduler tick;
// delivery is tracked by s.inflight so a graceful shutdown waits for it.
func (s *Scheduler) notifyRun(d *model.DAG, run *model.DagRun, final model.RunState, finishedAt time.Time, tis []*model.TaskInstance) {
	if d.NotifyURL == "" {
		return
	}
	ev := ""
	switch final {
	case model.RunSuccess:
		ev = "success"
	case model.RunFailed, model.RunCancelled, model.RunTimedOut:
		ev = "failure" // cancelled/failed/timed-out are all non-success for alerting
	}
	if ev == "" {
		return
	}
	// A dagrun_timeout kill always alerts when a webhook is configured — the operator
	// opted in by setting the timeout, same as SLA — so it is NOT gated by notify_on
	// (unlike a normal success/failure finalize, which requires the event to be listed).
	if final != model.RunTimedOut {
		want := false
		for _, e := range d.NotifyOn {
			if e == ev {
				want = true
			}
		}
		if !want {
			return
		}
	}

	// Name every task that did not succeed so a failure alert points somewhere.
	// This includes cancelled tasks, which is the only kind present when a run
	// finalizes as RunCancelled (e.g. a partial per-task retry leaves one behind).
	var affected []string
	for _, ti := range tis {
		switch ti.State {
		case model.TaskFailed, model.TaskUpstreamFailed, model.TaskCancelled, model.TaskTimedOut:
			affected = append(affected, ti.TaskID)
		}
	}
	dur := int64(0)
	if run.StartedAt != nil {
		if d := finishedAt.Sub(*run.StartedAt).Milliseconds(); d > 0 {
			dur = d
		}
	}
	summary := fmt.Sprintf("cronova · %s · run %s finished: %s", d.DagID, run.RunID, final)
	if len(affected) > 0 {
		summary += fmt.Sprintf(" (tasks: %v)", affected)
	}
	p := notifyPayload{
		Text: summary, DagID: d.DagID, RunID: run.RunID, State: string(final),
		LogicalDate: run.LogicalDate.UTC().Format(time.RFC3339), FinishedAt: finishedAt.UTC().Format(time.RFC3339),
		DurationMS: dur, FailedTasks: affected,
	}
	if run.StartedAt != nil {
		p.StartedAt = run.StartedAt.UTC().Format(time.RFC3339)
	}
	s.postNotify(d.NotifyURL, d.NotifyFormat, p)
}

// notifyDeadline fires a soft SLA-miss alert mid-run (the run keeps going). kind
// is "sla_miss" (run) or "task_sla_miss" (a specific task); taskID is set only for
// the latter. It fires whenever a webhook is configured — setting the threshold is
// itself the opt-in — independent of notify_on (which gates finalize alerts).
func (s *Scheduler) notifyDeadline(d *model.DAG, run *model.DagRun, kind, taskID string, thresholdSec int, elapsed time.Duration) {
	if d.NotifyURL == "" {
		return
	}
	summary := fmt.Sprintf("cronova · %s · run %s missed SLA (%ds)", d.DagID, run.RunID, thresholdSec)
	if taskID != "" {
		summary = fmt.Sprintf("cronova · %s · run %s task %s missed SLA (%ds)", d.DagID, run.RunID, taskID, thresholdSec)
	}
	p := notifyPayload{
		Text: summary, DagID: d.DagID, RunID: run.RunID, State: kind, TaskID: taskID,
		LogicalDate:  run.LogicalDate.UTC().Format(time.RFC3339),
		ThresholdSec: thresholdSec, ElapsedMS: elapsed.Milliseconds(),
	}
	if run.StartedAt != nil {
		p.StartedAt = run.StartedAt.UTC().Format(time.RFC3339)
	}
	s.postNotify(d.NotifyURL, d.NotifyFormat, p)
}

// notifyBody renders the webhook body for the DAG's notify.format. "raw" (or
// empty) sends the full structured payload; the chat formats wrap the summary
// text in each platform's incoming-webhook envelope so the message renders
// without a relay service.
func notifyBody(format string, p notifyPayload) []byte {
	var v any
	switch format {
	case "slack":
		v = map[string]string{"text": p.Text}
	case "feishu":
		v = map[string]any{"msg_type": "text", "content": map[string]string{"text": p.Text}}
	case "dingtalk":
		v = map[string]any{"msgtype": "text", "text": map[string]string{"content": p.Text}}
	default: // "", "raw"
		v = p
	}
	body, _ := json.Marshal(v)
	return body
}

// postNotify delivers a payload to the webhook asynchronously (best-effort,
// tracked by s.inflight for graceful shutdown). It snapshots everything the
// goroutine needs and logs only the host — never the secret-bearing URL.
func (s *Scheduler) postNotify(rawURL, format string, p notifyPayload) {
	url, runID, host, state := rawURL, p.RunID, notifyHost(rawURL), p.State
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		body := notifyBody(format, p)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			s.log.Error("notify build", "run", runID, "host", host, "err", stripURL(err))
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.notifyClient.Do(req)
		if err != nil {
			s.log.Error("notify post", "run", runID, "host", host, "err", stripURL(err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			s.log.Warn("notify non-2xx", "run", runID, "host", host, "status", resp.StatusCode)
		} else {
			s.log.Info("notify sent", "run", runID, "state", state)
		}
	}()
}

// stripURL unwraps a *url.Error so the secret-bearing request URL (which Go
// embeds verbatim in the error string, e.g. `Post "https://.../SECRET": EOF`)
// never reaches the log sink; the inner error still carries host:port + cause.
func stripURL(err error) error {
	var ue *neturl.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// notifyHost extracts host[:port] for logging, so the URL's secret-bearing path
// (Slack/Feishu tokens) never reaches the log sink.
func notifyHost(raw string) string {
	if u, err := neturl.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "?"
}
