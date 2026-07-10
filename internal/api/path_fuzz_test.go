package api

import (
	"path/filepath"
	"testing"
)

func FuzzCleanRelPath(f *testing.F) {
	for _, seed := range []string{"main.py", "pkg/util.py", "../escape", "/absolute", `..\escape`, "."} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		rel, err := cleanRelPath(input)
		if err != nil {
			return
		}
		if filepath.IsAbs(rel) {
			t.Fatalf("accepted absolute path %q from %q", rel, input)
		}
		root := filepath.Join(string(filepath.Separator), "cronova-fuzz-root")
		if dst := filepath.Join(root, rel); !withinDir(root, dst) {
			t.Fatalf("accepted escaping path %q from %q", rel, input)
		}
	})
}
