// Package datetmpl evaluates the date-expression grammar shared by command
// templates ({{ logical_date - 7d | %Y%m%d }}) and cross-DAG dependent
// offsets (depends_on_dag.offset):
//
//	logical_date[.anchor][ ±N<unit> ]... [| format]
//
//	anchor: month_start | month_end | week_start | week_end   (weeks start Monday)
//	unit:   d (days) | h (hours) | w (weeks) | mo (months)
//	format: strftime subset — %Y %y %m %d %H %M %S %%
//
// The anchor binds to the base name and applies first; offsets then apply
// left to right. Day/week/month offsets are calendar arithmetic (AddDate — a
// +1d across a DST change keeps the wall clock), hour offsets are absolute
// durations. Anything that does not parse completely evaluates to not-ok, so
// a typo stays visible instead of silently becoming an empty string.
package datetmpl

import (
	"strconv"
	"strings"
	"time"
)

// Eval evaluates a full expression ("logical_date..." / "logical_datetime...")
// against t and renders it (default format: date-only for logical_date,
// RFC3339 for logical_datetime).
func Eval(t time.Time, expr string) (string, bool) {
	rest, isDatetime := strings.CutPrefix(expr, "logical_datetime")
	if !isDatetime {
		var ok bool
		rest, ok = strings.CutPrefix(expr, "logical_date")
		if !ok {
			return "", false
		}
	}
	rest, format, hasFormat := cutFormat(rest)
	shifted, ok := Shift(t, rest)
	if !ok {
		return "", false
	}
	if hasFormat {
		return formatStrftime(shifted, format)
	}
	if isDatetime {
		return shifted.Format(time.RFC3339), true
	}
	return shifted.Format("2006-01-02"), true
}

// Shift applies an anchor/offset suffix (everything after the base name and
// before any "| format") to t: "", " - 1d", ".month_start - 1mo", …
// It is what depends_on_dag.offset evaluates through.
func Shift(t time.Time, suffix string) (time.Time, bool) {
	rest := suffix
	// Anchor: must sit directly on the base name (".month_end ...").
	if after, ok := strings.CutPrefix(strings.TrimLeft(rest, ""), "."); ok {
		name := after
		if i := strings.IndexFunc(after, func(r rune) bool {
			return !(r == '_' || (r >= 'a' && r <= 'z'))
		}); i >= 0 {
			name, rest = after[:i], after[i:]
		} else {
			rest = ""
		}
		var ok2 bool
		if t, ok2 = applyAnchor(t, name); !ok2 {
			return t, false
		}
	}
	for rest = strings.TrimSpace(rest); rest != ""; rest = strings.TrimSpace(rest) {
		var ok bool
		if t, rest, ok = applyOffset(t, rest); !ok {
			return t, false
		}
	}
	return t, true
}

// cutFormat splits "expr | format" on the first '|'. The format part keeps
// interior spacing ("%Y-%m-%d %H:%M") but is trimmed at both ends.
func cutFormat(s string) (expr, format string, ok bool) {
	expr, format, ok = strings.Cut(s, "|")
	return strings.TrimRight(expr, " \t"), strings.TrimSpace(format), ok
}

func applyAnchor(t time.Time, name string) (time.Time, bool) {
	y, m, d := t.Date()
	loc := t.Location()
	midnight := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	}
	switch name {
	case "month_start":
		return midnight(y, m, 1), true
	case "month_end":
		return midnight(y, m, 1).AddDate(0, 1, -1), true
	case "week_start":
		return midnight(y, m, d).AddDate(0, 0, -int((t.Weekday()+6)%7)), true
	case "week_end":
		return midnight(y, m, d).AddDate(0, 0, 6-int((t.Weekday()+6)%7)), true
	}
	return t, false
}

// applyOffset consumes one leading ±N<unit> token and returns the remainder.
func applyOffset(t time.Time, s string) (time.Time, string, bool) {
	sign := 1
	switch s[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return t, s, false
	}
	s = strings.TrimSpace(s[1:])
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return t, s, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return t, s, false
	}
	n *= sign
	s = s[i:]
	j := 0
	for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '+' && s[j] != '-' {
		j++
	}
	unit, rest := s[:j], s[j:]
	switch unit {
	case "d":
		return t.AddDate(0, 0, n), rest, true
	case "w":
		return t.AddDate(0, 0, 7*n), rest, true
	case "mo":
		return t.AddDate(0, n, 0), rest, true
	case "h":
		return t.Add(time.Duration(n) * time.Hour), rest, true
	}
	return t, rest, false
}

// formatStrftime renders t through a strftime-style format, expanding tokens
// directly rather than via a Go layout — literal text (including digits,
// which Go layouts would misread as tokens) passes through untouched. An
// unknown %-token rejects the whole expression.
func formatStrftime(t time.Time, f string) (string, bool) {
	pad2 := func(b *strings.Builder, n int) {
		if n < 10 {
			b.WriteByte('0')
		}
		b.WriteString(strconv.Itoa(n))
	}
	var b strings.Builder
	for i := 0; i < len(f); i++ {
		if f[i] != '%' {
			b.WriteByte(f[i])
			continue
		}
		i++
		if i >= len(f) {
			return "", false
		}
		switch f[i] {
		case 'Y':
			b.WriteString(strconv.Itoa(t.Year()))
		case 'y':
			pad2(&b, t.Year()%100)
		case 'm':
			pad2(&b, int(t.Month()))
		case 'd':
			pad2(&b, t.Day())
		case 'H':
			pad2(&b, t.Hour())
		case 'M':
			pad2(&b, t.Minute())
		case 'S':
			pad2(&b, t.Second())
		case '%':
			b.WriteByte('%')
		default:
			return "", false
		}
	}
	return b.String(), true
}

// ValidOffset reports whether an anchor/offset suffix parses (used by the DAG
// parser to validate depends_on_dag.offset at save time).
func ValidOffset(suffix string) bool {
	_, ok := Shift(time.Unix(0, 0).UTC(), suffix)
	return ok
}
