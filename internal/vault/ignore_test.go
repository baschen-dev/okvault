package vault

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestIgnoreCases(t *testing.T) {
	src, err := os.ReadFile("testdata/vaultignore-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Groups []struct {
			Rules string `json:"rules"`
			Cases []struct {
				Path    string `json:"path"`
				Dir     bool   `json:"dir"`
				Ignored bool   `json:"ignored"`
			} `json:"cases"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(src, &f); err != nil {
		t.Fatal(err)
	}
	for gi, g := range f.Groups {
		ig := ParseIgnore(g.Rules)
		for _, c := range g.Cases {
			if got := ig.Match(c.Path, c.Dir); got != c.Ignored {
				t.Errorf("group %d: Match(%q, dir=%v) = %v, want %v", gi, c.Path, c.Dir, got, c.Ignored)
			}
		}
	}
}

func TestVaultIgnoreSkipsFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".vaultignore", "# not knowledge\nscratch/\n*.wip.md\n")
	write(t, root, "a.md", "---\ntype: Reference\ntitle: Alpha\n---\nSee [s](./scratch/s.md) and [w](./b.wip.md).\n")
	write(t, root, "b.wip.md", "---\ntype: Reference\n---\n")
	write(t, root, "scratch/s.md", "---\ntype: Reference\n---\n")
	write(t, root, "scratch/deep/t.md", "---\ntype: Reference\n---\n")
	v, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	s := v.Snapshot()
	if len(s.Concepts) != 1 || s.Concepts["a.md"] == nil {
		t.Fatalf("concepts = %v, want only a.md", keys(s.Concepts))
	}
	if s.Dirs["scratch"] {
		t.Error("ignored folder listed as a concept directory")
	}
	if len(s.Broken["a.md"]) != 0 {
		t.Errorf("links into ignored files reported broken: %v", s.Broken["a.md"])
	}
	_, err = v.Write(WriteRequest{Path: "scratch/new.md", Type: "Reference", Title: "New", Body: "x"}, time.Now())
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("write into an ignored folder: err = %v, want ErrInvalidPath", err)
	}
	// Removing the rules brings the files back on reload.
	if err := os.Remove(root + "/.vaultignore"); err != nil {
		t.Fatal(err)
	}
	if s, _ = v.Reload(); len(s.Concepts) != 4 {
		t.Errorf("after removing .vaultignore: %d concepts, want 4", len(s.Concepts))
	}
}

func TestRelevantIgnoreFile(t *testing.T) {
	if !relevant("/v", "/v/.vaultignore") {
		t.Error(".vaultignore changes must reload the vault")
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
