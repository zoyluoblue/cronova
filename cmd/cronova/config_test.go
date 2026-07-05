package main

import "testing"

// TestParseBool locks in the fail-safe contract: recognized truthy/falsy tokens
// parse as expected (case-insensitive, trimmed), and anything unrecognized is
// reported invalid so callers keep their secure default instead of failing open.
func TestParseBool(t *testing.T) {
	cases := []struct {
		in         string
		val, valid bool
	}{
		{"1", true, true}, {"true", true, true}, {"yes", true, true},
		{"on", true, true}, {"y", true, true}, {"enabled", true, true},
		{"TRUE", true, true}, {"True", true, true}, {"  true  ", true, true},
		{"0", false, true}, {"false", false, true}, {"no", false, true},
		{"off", false, true}, {"n", false, true}, {"disabled", false, true},
		{"FALSE", false, true},
		// unrecognized / blank -> invalid, so the caller keeps its default
		{"", false, false}, {"maybe", false, false}, {"2", false, false},
		{"tru", false, false}, {"enable-auth", false, false},
	}
	for _, c := range cases {
		val, valid := parseBool(c.in)
		if val != c.val || valid != c.valid {
			t.Errorf("parseBool(%q) = (%v,%v), want (%v,%v)", c.in, val, valid, c.val, c.valid)
		}
	}
}
