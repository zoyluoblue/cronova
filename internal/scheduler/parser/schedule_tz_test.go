package parser

import (
	"testing"
	"time"
)

// The schedule field supports robfig/cron's TZ prefix — "CRON_TZ=Asia/Shanghai
// 0 2 * * *" fires at 02:00 Shanghai time, not 02:00 UTC. This guards the
// documented behavior against a parser reconfiguration losing it.
func TestScheduleCronTZPrefix(t *testing.T) {
	s, err := ParseSchedule("CRON_TZ=Asia/Shanghai 0 2 * * *")
	if err != nil {
		t.Fatalf("CRON_TZ prefix rejected: %v", err)
	}
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	next := s.Next(time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC))
	if got := next.In(sh); got.Hour() != 2 || got.Minute() != 0 {
		t.Fatalf("next in Shanghai = %s, want 02:00 local", got)
	}
	// an invalid zone is a parse error, not a silent UTC fallback
	if _, err := ParseSchedule("CRON_TZ=Not/AZone 0 2 * * *"); err == nil {
		t.Fatal("invalid CRON_TZ zone should be rejected")
	}
}
