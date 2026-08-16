package scheduler

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/zoyluo/cronova/internal/metrics"
)

// Email alert delivery: a notify channel whose URL is mailto:a@x.com,b@y.com
// is sent through the instance-level SMTP relay (Options.SMTP) instead of an
// HTTP POST. Standard library only — port 465 dials implicit TLS, any other
// port upgrades via STARTTLS, and plaintext delivery is refused unless the
// operator explicitly allowed it (lab relays).

// mailtoRecipients extracts the address list from a mailto: URL. Header
// parameters (?subject=...) are dropped — the subject is always the alert
// summary.
func mailtoRecipients(rawURL string) []string {
	s := rawURL[len("mailto:"):]
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" && strings.Contains(a, "@") {
			out = append(out, a)
		}
	}
	return out
}

// emailBody renders the plain-text message: the summary first, then the
// structured fields an on-call reader wants without opening the console.
func emailBody(p notifyPayload) string {
	var b strings.Builder
	b.WriteString(p.Text)
	b.WriteString("\r\n\r\n")
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%-13s %s\r\n", k+":", v)
		}
	}
	line("dag", p.DagID)
	line("run", p.RunID)
	line("state", p.State)
	line("logical date", p.LogicalDate)
	line("started", p.StartedAt)
	line("finished", p.FinishedAt)
	if p.DurationMS > 0 {
		line("duration", (time.Duration(p.DurationMS) * time.Millisecond).String())
	}
	if len(p.FailedTasks) > 0 {
		line("failed tasks", strings.Join(p.FailedTasks, ", "))
	}
	if p.TaskID != "" {
		line("task", p.TaskID)
	}
	if p.ThresholdSec > 0 {
		line("threshold", (time.Duration(p.ThresholdSec) * time.Second).String())
	}
	return b.String()
}

// buildMessage assembles the RFC 5322 message. The subject is sanitized
// against header injection (payload text can embed operator-provided ids).
func buildMessage(from string, to []string, subject, body string) []byte {
	clean := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, subject)
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", clean)
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

// postEmail delivers a payload by mail asynchronously with the same retry
// posture as webhooks: transient failures (dial, greeting, 4xx-class SMTP
// hiccups) retry on the notifyRetryDelays ladder; a missing SMTP config or
// empty recipient list is a configuration error and is not retried.
func (s *Scheduler) postEmail(rawURL string, p notifyPayload) {
	to := mailtoRecipients(rawURL)
	runID, state := p.RunID, p.State
	s.inflight.Add(1)
	go func() {
		defer s.inflight.Done()
		if !s.opts.SMTP.Enabled() {
			metrics.IncNotifyFailure()
			s.log.Error("email notify dropped: smtp not configured", "run", runID, "state", state)
			return
		}
		if len(to) == 0 {
			metrics.IncNotifyFailure()
			s.log.Error("email notify dropped: no recipients in mailto:", "run", runID)
			return
		}
		msg := buildMessage(s.opts.SMTP.sender(), to, p.Text, emailBody(p))
		for try := 0; ; try++ {
			err := sendSMTP(s.opts.SMTP, to, msg)
			if err == nil {
				s.log.Info("email notify sent", "run", runID, "state", state, "recipients", len(to), "try", try+1)
				return
			}
			if try >= len(notifyRetryDelays) {
				metrics.IncNotifyFailure()
				s.log.Error("email notify failed", "run", runID, "host", s.opts.SMTP.Host, "tries", try+1, "err", err)
				return
			}
			s.log.Warn("email notify retrying", "run", runID, "host", s.opts.SMTP.Host, "try", try+1, "err", err)
			time.Sleep(notifyRetryDelays[try])
		}
	}()
}

func (c SMTPConfig) sender() string {
	if c.From != "" {
		return c.From
	}
	return c.Username
}

func (c SMTPConfig) addr() string {
	port := c.Port
	if port == 0 {
		port = 587
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// sendSMTP performs one complete delivery attempt.
func sendSMTP(cfg SMTPConfig, to []string, msg []byte) error {
	addr := cfg.addr()
	implicitTLS := strings.HasSuffix(addr, ":465")

	var cl *smtp.Client
	if implicitTLS {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: cfg.Host})
		if err != nil {
			return fmt.Errorf("dial tls: %w", err)
		}
		if cl, err = smtp.NewClient(conn, cfg.Host); err != nil {
			conn.Close()
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		if cl, err = smtp.NewClient(conn, cfg.Host); err != nil {
			conn.Close()
			return err
		}
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
				cl.Close()
				return fmt.Errorf("starttls: %w", err)
			}
		} else if !cfg.AllowPlaintext {
			cl.Close()
			return fmt.Errorf("server does not offer STARTTLS (set smtp.allow_plaintext for lab relays)")
		}
	}
	defer cl.Close()

	if cfg.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := cl.Mail(cfg.sender()); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	for _, rcpt := range to {
		if err := cl.Rcpt(rcpt); err != nil {
			return fmt.Errorf("rcpt %s: %w", rcpt, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
