package parser

import "testing"

// foreach expands into per-item tasks with {{ item }}/{{ item_index }}
// substituted, and downstream deps on the original id cover every shard.
func TestForeachExpansion(t *testing.T) {
	d, err := Parse([]byte(`
dag_id: fan
tasks:
  - id: split
    command: echo start
  - id: work
    foreach: ["alpha", "beta", "gamma"]
    command: process --part {{ item }} --idx {{ item_index }}
    deps: [split]
  - id: join
    command: echo done
    deps: [work]
`))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]struct {
		cmd  string
		deps []string
	}{}
	for _, tk := range d.Tasks {
		byID[tk.ID] = struct {
			cmd  string
			deps []string
		}{tk.Command, tk.Deps}
	}
	if len(d.Tasks) != 5 { // split + 3 shards + join
		t.Fatalf("task count = %d, want 5", len(d.Tasks))
	}
	if byID["work_1"].cmd != "process --part beta --idx 1" {
		t.Fatalf("shard cmd = %q", byID["work_1"].cmd)
	}
	if got := byID["work_2"].deps; len(got) != 1 || got[0] != "split" {
		t.Fatalf("shard deps = %v", got)
	}
	join := byID["join"].deps
	if len(join) != 3 || join[0] != "work_0" || join[2] != "work_2" {
		t.Fatalf("join deps = %v, want all shards", join)
	}
	// an over-wide fan-out is rejected, not truncated
	big := "dag_id: too\ntasks:\n  - id: w\n    command: echo x\n    foreach: ["
	for i := 0; i < 1001; i++ {
		big += `"x",`
	}
	big += "]\n"
	if _, err := Parse([]byte(big)); err == nil {
		t.Fatal("oversized foreach accepted")
	}
}
