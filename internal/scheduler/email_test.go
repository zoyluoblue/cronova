package scheduler

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zoyluo/cronova/internal/model"
)

// fakeSMTP is a minimal plaintext SMTP server (no STARTTLS, no AUTH) that
// records delivered messages. Tests run with AllowPlaintext.
func fakeSMTP(t *testing.T) (host string, port int, msgs func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				w := func(s string) { c.Write([]byte(s + "\r\n")) }
				w("220 test ESMTP")
				sc := bufio.NewScanner(c)
				var data strings.Builder
				inData := false
				for sc.Scan() {
					line := sc.Text()
					if inData {
						if line == "." {
							mu.Lock()
							got = append(got, data.String())
							mu.Unlock()
							data.Reset()
							inData = false
							w("250 OK")
							continue
						}
						data.WriteString(line + "\r\n")
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						w("250-test")
						w("250 SIZE 1000000")
					case strings.HasPrefix(line, "HELO"):
						w("250 test")
					case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
						w("250 OK")
					case strings.HasPrefix(line, "DATA"):
						inData = true
						w("354 go ahead")
					case strings.HasPrefix(line, "QUIT"):
						w("221 bye")
						return
					default:
						w("250 OK")
					}
				}
			}(conn)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(got))
		copy(out, got)
		return out
	}
}

func TestMailtoRecipients(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"mailto:a@x.com", []string{"a@x.com"}},
		{"mailto:a@x.com,b@y.com", []string{"a@x.com", "b@y.com"}},
		{"mailto:a@x.com, b@y.com ", []string{"a@x.com", "b@y.com"}},
		{"mailto:a@x.com?subject=hi", []string{"a@x.com"}},
		{"mailto:", nil},
		{"mailto:not-an-address", nil},
	}
	for _, c := range cases {
		got := mailtoRecipients(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("mailtoRecipients(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestBuildMessageSanitizesSubject: CRLF in the subject must not become extra
// headers (header injection via a crafted dag id).
func TestBuildMessageSanitizesSubject(t *testing.T) {
	msg := string(buildMessage("f@x.com", []string{"t@y.com"}, "evil\r\nBcc: victim@z.com", "body"))
	if strings.Contains(msg, "\r\nBcc:") || strings.Contains(msg, "\nBcc:") {
		t.Fatalf("subject CRLF produced a separate header line:\n%s", msg)
	}
	if !strings.Contains(msg, "Subject: evil  Bcc: victim@z.com") {
		t.Errorf("sanitized subject missing:\n%s", msg)
	}
}

// TestEmailNotifyOnFailure: a mailto: notify target delivers a real SMTP
// message through the configured relay when the run fails.
func TestEmailNotifyOnFailure(t *testing.T) {
	host, port, msgs := fakeSMTP(t)
	s := newTestScheduler(t)
	s.opts.SMTP = SMTPConfig{Host: host, Port: port, From: "cronova@test", AllowPlaintext: true}
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "maildag", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyURL: "mailto:oncall@example.com,backup@example.com", NotifyOn: []string{"failure"},
		Tasks: []model.Task{{ID: "boom", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "maildag", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	s.WaitInflight()

	got := msgs()
	if len(got) != 1 {
		t.Fatalf("got %d mails, want 1", len(got))
	}
	m := got[0]
	for _, want := range []string{
		"To: oncall@example.com, backup@example.com",
		"From: cronova@test",
		"Subject: cronova", // summary line
		"maildag",
		"boom",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mail missing %q:\n%s", want, m)
		}
	}
}

// TestAlertGroupFanout: a DAG referencing an alert group alerts every channel
// in the group (here: two webhooks), and the group wins over notify_url.
func TestAlertGroupFanout(t *testing.T) {
	url1, bodies1 := captureHook(t)
	url2, bodies2 := captureHook(t)
	urlOwn, bodiesOwn := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	if err := s.store.UpsertAlertGroup(ctx, &model.AlertGroup{
		Name: "oncall",
		Channels: []model.NotifyChannel{
			{URL: url1, Format: "raw"},
			{URL: url2, Format: "slack"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	dag := &model.DAG{
		DagID: "groupdag", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyGroup: "oncall", NotifyURL: urlOwn, NotifyOn: []string{"failure"},
		Tasks: []model.Task{{ID: "boom", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "groupdag", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	s.WaitInflight()

	if n := len(bodies1()); n != 1 {
		t.Errorf("channel 1 got %d posts, want 1", n)
	}
	if n := len(bodies2()); n != 1 {
		t.Errorf("channel 2 got %d posts, want 1", n)
	}
	if n := len(bodiesOwn()); n != 0 {
		t.Errorf("notify_url got %d posts, want 0 (group wins)", n)
	}
	if b := bodies2(); len(b) == 1 && !strings.Contains(string(b[0]), `"text"`) {
		t.Errorf("channel 2 not slack-formatted: %s", b[0])
	}
}

// TestAlertGroupDanglingFallsBack: a missing group must not lose the alert —
// delivery falls back to the DAG's own notify_url.
func TestAlertGroupDanglingFallsBack(t *testing.T) {
	urlOwn, bodiesOwn := captureHook(t)
	s := newTestScheduler(t)
	ctx := context.Background()
	dag := &model.DAG{
		DagID: "dangling", MaxActiveRuns: 1, StartDate: time.Now().UTC(),
		NotifyGroup: "no-such-group", NotifyURL: urlOwn, NotifyOn: []string{"failure"},
		Tasks: []model.Task{{ID: "boom", Command: "exit 1", Pool: model.DefaultPoolName}},
	}
	if err := s.registerDAG(ctx, dag); err != nil {
		t.Fatal(err)
	}
	runID, _ := s.TriggerManual(ctx, "dangling", nil)
	if run := s.driveToTerminal(t, ctx, runID, 40); run.State != model.RunFailed {
		t.Fatalf("run = %s, want failed", run.State)
	}
	s.WaitInflight()
	if n := len(bodiesOwn()); n != 1 {
		t.Errorf("fallback notify_url got %d posts, want 1", n)
	}
}
