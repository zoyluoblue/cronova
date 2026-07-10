package parser

import "testing"

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"dag_id: demo\ntasks:\n  - id: hello\n    command: echo hi\n",
		"dag_id: cycle\ntasks:\n  - id: a\n    command: true\n    deps: [b]\n  - id: b\n    command: true\n    deps: [a]\n",
		"not: [valid",
		"",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		_, _ = Parse(input)
	})
}
