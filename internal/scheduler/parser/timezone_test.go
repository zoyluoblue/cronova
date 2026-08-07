package parser

import (
	"testing"
	"time"
)

// A timezone'd cron means wall-clock time THERE: 02:00 Asia/Shanghai is 18:00
// UTC the previous day.
func TestParseScheduleInTimezone(t *testing.T) {
	sched, err := ParseScheduleIn("0 2 * * *", "Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	next := sched.Next(anchor).UTC()
	want := time.Date(2026, 8, 7, 18, 0, 0, 0, time.UTC) // 2026-08-08 02:00 CST
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
	// empty tz keeps UTC semantics
	utcSched, _ := ParseScheduleIn("0 2 * * *", "")
	if got := utcSched.Next(anchor).UTC(); !got.Equal(time.Date(2026, 8, 7, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("utc next = %s", got)
	}
	// bad zone rejected at Parse time via dag validation
	if _, err := Parse([]byte("dag_id: tz_bad\nschedule: '0 2 * * *'\ntimezone: Not/AZone\ntasks:\n  - id: a\n    command: echo hi\n")); err == nil {
		t.Fatal("invalid timezone accepted")
	}
	// good zone accepted and carried on the model
	d, err := Parse([]byte("dag_id: tz_ok\nschedule: '0 2 * * *'\ntimezone: Asia/Shanghai\nstart_date: \"2026-01-01\"\ntasks:\n  - id: a\n    command: echo hi\n"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Timezone != "Asia/Shanghai" {
		t.Fatalf("timezone = %q", d.Timezone)
	}
	// date-only start_date interpreted in the DAG's zone: midnight CST = 16:00 UTC prev day
	if want := time.Date(2025, 12, 31, 16, 0, 0, 0, time.UTC); !d.StartDate.Equal(want) {
		t.Fatalf("start_date = %s, want %s", d.StartDate, want)
	}
}
