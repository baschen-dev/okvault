package vault

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) *Vault {
	t.Helper()
	root := t.TempDir()
	write(t, root, "a.md", "---\ntype: Reference\ntitle: Alpha\n---\nSee [b](./sub/b.md) and [[Beta]] and [x](./missing.md).\n")
	write(t, root, "sub/b.md", "---\ntype: Reference\ntitle: Beta\ndescription: The b note.\n---\nBack to [a](../a.md).\n")
	write(t, root, "bad.md", "---\ntitle: [unclosed\n---\n")
	write(t, root, "plain.md", "A plain Obsidian note, see [[Alpha]].\n")
	write(t, root, ".obsidian/x.md", "---\ntype: X\n---\n")
	write(t, root, "_templates/t.md", "---\ntype: FAQ\n---\n")
	v, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLoadGraph(t *testing.T) {
	v := fixture(t)
	s := v.Snapshot()
	if len(s.Concepts) != 3 {
		t.Fatalf("want 3 concepts (a, sub/b and the plain note), got %v", s.Sorted())
	}
	if got := strings.Join(s.Links["a.md"], ","); got != "sub/b.md" {
		t.Fatalf("links a.md = %q (wikilink and md link to the same note should dedupe)", got)
	}
	if got := strings.Join(s.Broken["a.md"], ","); got != "missing.md" {
		t.Fatalf("broken = %q", got)
	}
	if c := s.Concepts["plain.md"]; c == nil || c.Type != "" || c.Title != "Plain" {
		t.Fatalf("plain note = %+v", c)
	}
	if got := strings.Join(s.Backlinks["a.md"], ","); got != "plain.md,sub/b.md" && got != "sub/b.md,plain.md" {
		t.Fatalf("a plain note's wikilink should count as a backlink, got %q", got)
	}
	if len(s.Issues) != 2 || s.Issues[0].Path != "bad.md" || s.Issues[0].Warning || s.Issues[1].Path != "plain.md" || !s.Issues[1].Warning {
		t.Fatalf("issues = %+v", s.Issues)
	}
}

func TestCleanPath(t *testing.T) {
	bad := []string{"../x.md", "/abs.md", "C:/x.md", "a/../../x.md", "x.txt", "index.md", "sub/log.md", ".obsidian/a.md", "_templates/a.md", ""}
	for _, p := range bad {
		if _, err := CleanPath(p); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("CleanPath(%q) err=%v, want ErrInvalidPath", p, err)
		}
	}
	if got, err := CleanPath(`faq\q.md`); err != nil || got != "faq/q.md" {
		t.Errorf("got %q %v", got, err)
	}
}

func TestWriteCreatesIndexAndLog(t *testing.T) {
	v := fixture(t)
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	res, err := v.Write(WriteRequest{Path: "faq/new/q.md", Type: "FAQ", Title: "Why?", Body: "Because.", Mode: "create", Author: "test"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Index != "faq/new/index.md" {
		t.Fatalf("res = %+v", res)
	}
	src, _ := v.ReadFile("faq/new/q.md")
	for _, want := range []string{"type: FAQ", "title: Why?", "by: test", "at: \"2026-11-22T09:00:00Z\"", "Because."} {
		if !strings.Contains(string(src), want) {
			t.Errorf("note missing %q:\n%s", want, src)
		}
	}
	idx, _ := v.ReadFile("index.md")
	if !strings.Contains(string(idx), "./faq/index.md") {
		t.Errorf("root index should list faq section:\n%s", idx)
	}
	faqIdx, _ := v.ReadFile("faq/index.md")
	if !strings.Contains(string(faqIdx), "./new/index.md") {
		t.Errorf("faq index should list new section:\n%s", faqIdx)
	}
	log, _ := v.ReadFile("log.md")
	if !strings.Contains(string(log), "## 2026-11-22") || !strings.Contains(string(log), "**Creation** [Why?](./faq/new/q.md)") {
		t.Errorf("log:\n%s", log)
	}
	if _, err := v.Write(WriteRequest{Path: "faq/new/q.md", Type: "FAQ", Mode: "create"}, now); err == nil {
		t.Error("create over existing note should fail")
	}
	if _, err := v.Write(WriteRequest{Path: "nope.md", Mode: "create"}, now); err == nil {
		t.Error("create without type should fail")
	}
}

func TestUpdatePreservesUnknownKeysAndHandIndex(t *testing.T) {
	v := fixture(t)
	write(t, v.Root, "sub/c.md", "---\ntype: Tool\ncustom: keep-me\n---\nold\n")
	write(t, v.Root, "sub/index.md", "# Hand written\n\n* [Beta](./b.md)\n")
	if _, err := v.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Write(WriteRequest{Path: "sub/c.md", Description: "New desc.", Mode: "update"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	src, _ := v.ReadFile("sub/c.md")
	if !strings.Contains(string(src), "custom: keep-me") || !strings.Contains(string(src), "type: Tool") || !strings.Contains(string(src), "old") {
		t.Errorf("update lost data:\n%s", src)
	}
	idx, _ := v.ReadFile("sub/index.md")
	if !strings.HasPrefix(string(idx), "# Hand written") {
		t.Errorf("hand-written index was overwritten:\n%s", idx)
	}
}

func TestLint(t *testing.T) {
	v := fixture(t)
	write(t, v.Root, "log.md", "# Log\n\n## 2026-01-01\n\n* x\n\n## 2026-02-01\n\n* y\n")
	fs := v.Lint()
	var errs, warns []string
	for _, f := range fs {
		if f.Severity == Error {
			errs = append(errs, f.Path+": "+f.Message)
		} else {
			warns = append(warns, f.Path+": "+f.Message)
		}
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "bad.md") || !strings.Contains(joined, "newest first") {
		t.Errorf("errors:\n%s", joined)
	}
	if strings.Contains(joined, "plain.md") {
		t.Errorf("a note without frontmatter should be a warning, not an error:\n%s", joined)
	}
	if w := strings.Join(warns, "\n"); !strings.Contains(w, "broken link: missing.md") || !strings.Contains(w, "plain.md: no frontmatter") {
		t.Errorf("warnings:\n%s", strings.Join(warns, "\n"))
	}
}

func TestWritePlainNoteNeedsType(t *testing.T) {
	v := fixture(t)
	if _, err := v.Write(WriteRequest{Path: "plain.md", Description: "d", Mode: "update"}, time.Now()); err == nil || !strings.Contains(err.Error(), "no `type`") {
		t.Fatalf("update without type: err = %v", err)
	}
	if _, err := v.Write(WriteRequest{Path: "plain.md", Type: "Reference", Mode: "update"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	src, _ := v.ReadFile("plain.md")
	if !strings.HasPrefix(string(src), "---\ntype: Reference\n") || !strings.Contains(string(src), "A plain Obsidian note, see [[Alpha]].") {
		t.Fatalf("plain note not converted:\n%s", src)
	}
	if is := v.Snapshot().Issues; len(is) != 1 || is[0].Path != "bad.md" {
		t.Fatalf("issues after conversion = %+v", is)
	}
}
