package okf

import (
	"path"
	"strings"
	"testing"
	"time"
)

func TestParseRoundTripPreservesUnknownKeys(t *testing.T) {
	src := "---\r\ntype: Playbook\r\ntitle: Release\r\ncustom:\r\n  nested: 1\r\ntags: [a, b]\r\n---\r\nBody [x](./y.md)\r\n"
	c, err := Parse("ops/release.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if c.Type != "Playbook" || c.Title != "Release" || len(c.Tags) != 2 {
		t.Fatalf("bad parse: %+v", c)
	}
	if err := c.Set("description", "How to release."); err != nil {
		t.Fatal(err)
	}
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"custom:", "nested: 1", "description: How to release.", "Body [x](./y.md)"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "\r") {
		t.Error("output should be LF only")
	}
}

func TestMarshalRequiresType(t *testing.T) {
	c := New("a.md", "")
	if _, err := c.Marshal(); err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestNoFrontmatter(t *testing.T) {
	if _, err := Parse("a.md", []byte("# hi\n")); err != ErrNoFrontmatter {
		t.Fatalf("got %v", err)
	}
}

func TestTitleFromPath(t *testing.T) {
	if got := TitleFromPath("tools/go-modules.md"); got != "Go modules" {
		t.Fatal(got)
	}
}

func TestExtractLinks(t *testing.T) {
	body := "See [a](/tools/go.md), `[e](./code.md)` [b](../ops/x.md#h), [c](https://e.com/z.md), [[Some Note|alias]]\n```\n[d](./ignored.md)\n```"
	var got []string
	for _, l := range ExtractLinks("faq/q.md", body) {
		got = append(got, l.Target)
	}
	if want := "tools/go.md ops/x.md Some Note"; strings.Join(got, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(got, " "), want)
	}
}

func TestRelLinkRoundTrip(t *testing.T) {
	cases := [][3]string{
		{"index.md", "tools/go.md", "./tools/go.md"},
		{"faq/index.md", "faq/a.md", "./a.md"},
		{"faq/q.md", "tools/go.md", "../tools/go.md"},
		{"a/b/c.md", "a/d.md", "../d.md"},
	}
	for _, c := range cases {
		if got := RelLink(c[0], c[1]); got != c[2] {
			t.Errorf("RelLink(%s,%s)=%s want %s", c[0], c[1], got, c[2])
		}
		if got := ResolvePath(path.Dir(c[0]), c[2]); got != c[1] {
			t.Errorf("resolve %s from %s = %s", c[2], c[0], got)
		}
	}
	if ResolvePath("a", "../../x.md") != "" {
		t.Error("escaping link should resolve to empty")
	}
}

func TestAppendLog(t *testing.T) {
	d1 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	d2 := d1.Add(24 * time.Hour)
	log := AppendLog(nil, d1, "Creation", "first")
	log = AppendLog(log, d2, "Update", "second")
	log = AppendLog(log, d2, "Update", "third")
	s := string(log)
	if strings.Index(s, "## 2026-11-22") > strings.Index(s, "## 2026-11-21") {
		t.Fatalf("newest day first violated:\n%s", s)
	}
	if strings.Index(s, "third") > strings.Index(s, "second") {
		t.Fatalf("newest entry first within day violated:\n%s", s)
	}
}

func TestRenderIndex(t *testing.T) {
	out := string(RenderIndex(".", []IndexEntry{{Path: "faq/a.md", Title: "A", Type: "FAQ", Description: "d"}}, []string{"faq"}, true))
	for _, want := range []string{`okf_version: "0.2"`, "# FAQ", "* [A](./faq/a.md) - d", "* [Faq](./faq/index.md)", GeneratedMarker} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
