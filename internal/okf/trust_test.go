package okf

import (
	"strings"
	"testing"
	"time"
)

func TestVerifiedForms(t *testing.T) {
	bare, err := Parse("a.md", []byte("---\ntype: X\nverified: {by: alice, at: 2026-01-02T03:04:05Z}\ngenerated: {by: bot, at: \"2026-02-01T00:00:00Z\"}\nstale_after: 2026-06-01\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	lv, ok := bare.LastVerified()
	if !ok || lv.By != "alice" || lv.At.Year() != 2026 {
		t.Fatalf("bare mapping: %+v %v", lv, ok)
	}
	if g, ok := bare.Generated(); !ok || g.By != "bot" || !g.At.After(lv.At) {
		t.Fatalf("generated: %+v", g)
	}
	if sa, present, ok := bare.StaleAfter(); !present || !ok || sa.Month() != time.June {
		t.Fatalf("unquoted date stale_after: %v %v %v", sa, present, ok)
	}
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := bare.AddVerified("bob", at); err != nil {
		t.Fatal(err)
	}
	if vs := bare.Verified(); len(vs) != 2 || vs[1].By != "bob" {
		t.Fatalf("append: %+v", vs)
	}
	out, _ := bare.Marshal()
	if !strings.Contains(string(out), "verified:\n  - by: alice") {
		t.Fatalf("verified must become a list:\n%s", out)
	}
	if _, present, ok := New("b.md", "X").StaleAfter(); present || ok {
		t.Fatal("absent stale_after")
	}
}

func TestNormalizeLog(t *testing.T) {
	// what git's union merge leaves when two branches logged on the same days
	merged := "# Log\n\n## 2026-11-22\n\n* **Creation** a\n\n## 2026-11-20\n\n* **Update** x\n\n## 2026-11-22\n\n* **Creation** b\n* **Creation** a\n\n## 2026-11-23\n\n* **Update** c\n"
	want := "# Log\n\n## 2026-11-23\n\n* **Update** c\n\n## 2026-11-22\n\n* **Creation** a\n* **Creation** b\n\n## 2026-11-20\n\n* **Update** x\n"
	if got := string(NormalizeLog([]byte(merged))); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if got := string(NormalizeLog([]byte(want))); got != want {
		t.Fatalf("not idempotent:\n%s", got)
	}
	odd := []byte("# Log\n\n## Someday\n\n* x\n")
	if string(NormalizeLog(odd)) != string(odd) {
		t.Fatal("non-date heading must be left alone")
	}
}
