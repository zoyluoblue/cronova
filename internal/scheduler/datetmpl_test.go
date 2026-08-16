package scheduler

import (
	"testing"
	"time"
)

// 2026-08-08 is a Saturday.
var dtBase = time.Date(2026, 8, 8, 10, 30, 0, 0, time.UTC)

func TestEvalDateExpr(t *testing.T) {
	cases := []struct {
		expr string
		want string
		ok   bool
	}{
		// Bare names (normally served by the base map, but must also work here).
		{"logical_date", "2026-08-08", true},
		{"logical_datetime", "2026-08-08T10:30:00Z", true},

		// Offsets.
		{"logical_date + 1d", "2026-08-09", true},
		{"logical_date - 7d", "2026-08-01", true},
		{"logical_date +2w", "2026-08-22", true},
		{"logical_date - 1mo", "2026-07-08", true},
		{"logical_datetime + 2h", "2026-08-08T12:30:00Z", true},
		{"logical_datetime - 12h", "2026-08-07T22:30:00Z", true},
		{"logical_date + 1mo - 1d", "2026-09-07", true},
		{"logical_date+1d", "2026-08-09", true}, // no spaces at all

		// Anchors (weeks start Monday; Aug 8 2026 is a Saturday).
		{"logical_date.month_start", "2026-08-01", true},
		{"logical_date.month_end", "2026-08-31", true},
		{"logical_date.week_start", "2026-08-03", true},
		{"logical_date.week_end", "2026-08-09", true},
		{"logical_date.month_start - 1d", "2026-07-31", true}, // last day of previous month
		{"logical_date.month_start - 1mo", "2026-07-01", true},

		// Formats.
		{"logical_date | %Y%m%d", "20260808", true},
		{"logical_date - 7d | %Y%m%d", "20260801", true},
		{"logical_date | %Y/%m/%d", "2026/08/08", true},
		{"logical_datetime | %Y-%m-%d %H:%M", "2026-08-08 10:30", true},
		{"logical_datetime + 2h | %H:%M:%S", "12:30:00", true},
		{"logical_date | %y%m", "2608", true},
		{"logical_date | 100%%", "100%", true},

		// Anchor on datetime keeps midnight and RFC3339 shape.
		{"logical_datetime.month_start", "2026-08-01T00:00:00Z", true},

		// Rejected: typos and unknown syntax must resolve to not-found so the
		// placeholder stays visible in the rendered command.
		{"logical_dates", "", false},
		{"logical_date.month_middle", "", false},
		{"logical_date ++ 1d", "", false},
		{"logical_date + d", "", false},
		{"logical_date + 1x", "", false},
		{"logical_date + 1m", "", false}, // months are "mo", minutes unsupported
		{"logical_date | %Q", "", false},
		{"logical_date extra", "", false},
	}
	for _, c := range cases {
		got, ok := evalDateExpr(dtBase, c.expr)
		if ok != c.ok || got != c.want {
			t.Errorf("evalDateExpr(%q) = (%q, %v), want (%q, %v)", c.expr, got, ok, c.want, c.ok)
		}
	}
}

func TestEvalDateExprTimezone(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// Stored UTC instant 2026-08-08T18:00Z is already Aug 9 in Shanghai; the
	// caller hands us the time in DAG-local form, and everything renders there.
	local := time.Date(2026, 8, 8, 18, 0, 0, 0, time.UTC).In(sh)
	if got, _ := evalDateExpr(local, "logical_date"); got != "2026-08-09" {
		t.Errorf("local render = %q, want 2026-08-09", got)
	}
	if got, _ := evalDateExpr(local, "logical_date.month_start"); got != "2026-08-01" {
		t.Errorf("month_start = %q", got)
	}
	if got, _ := evalDateExpr(local, "logical_datetime"); got != "2026-08-09T02:00:00+08:00" {
		t.Errorf("datetime = %q", got)
	}
}

func TestEvalDateExprDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// US spring forward 2026: Mar 8, 02:00 → 03:00. A +1d calendar offset
	// keeps the wall clock; a +24h duration offset lands an hour later.
	base := time.Date(2026, 3, 7, 12, 0, 0, 0, ny)
	if got, _ := evalDateExpr(base, "logical_datetime + 1d | %H:%M"); got != "12:00" {
		t.Errorf("+1d across DST = %q, want 12:00", got)
	}
	if got, _ := evalDateExpr(base, "logical_datetime + 24h | %H:%M"); got != "13:00" {
		t.Errorf("+24h across DST = %q, want 13:00", got)
	}
}

// TestRenderCommandDateExprs proves the full render path: date expressions
// resolve, unknown expressions and unrelated braces survive verbatim.
func TestRenderCommandDateExprs(t *testing.T) {
	resolve := func(key string) (string, bool) {
		if key == "logical_date" {
			return dtBase.Format("2006-01-02"), true
		}
		if len(key) > len("logical_date") && key[:len("logical_date")] == "logical_date" {
			return evalDateExpr(dtBase, key)
		}
		return "", false
	}
	in := `run.sh {{ logical_date }} {{ logical_date - 7d | %Y%m%d }} {{ logical_date | %Q }} {{ awk }} ${SHELL_VAR}`
	want := `run.sh 2026-08-08 20260801 {{ logical_date | %Q }} {{ awk }} ${SHELL_VAR}`
	if got := renderCommand(in, resolve); got != want {
		t.Errorf("renderCommand:\n got  %q\n want %q", got, want)
	}
}
