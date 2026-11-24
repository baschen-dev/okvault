package gaps

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// word-overlap stand-ins for search.Similarity / search.Normalize
func sim(a, b string) float64 {
	wa, wb := map[string]bool{}, map[string]bool{}
	for _, w := range strings.Fields(norm(a)) {
		wa[w] = true
	}
	for _, w := range strings.Fields(norm(b)) {
		wb[w] = true
	}
	n := 0
	for w := range wa {
		if wb[w] {
			n++
		}
	}
	if len(wa)+len(wb) == 0 {
		return 0
	}
	return 2 * float64(n) / float64(len(wa)+len(wb))
}

func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.Trim(s, "?! "))), " ")
}

func TestRecordReadDismiss(t *testing.T) {
	l := Open(filepath.Join(t.TempDir(), "sub", "gaps.jsonl"))
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	if err := l.Record("  how do we rotate the   jira token? ", "claude-code", t0); err != nil {
		t.Fatal(err)
	}
	_ = l.Record("", "x", t0) // ignored
	_ = l.Record(strings.Repeat("é", MaxQuestion+10), "x", t0.Add(time.Minute))
	_ = l.Dismiss("abc", t0.Add(2*time.Minute))
	qs, dismissed, err := l.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 || qs[0].Question != "how do we rotate the jira token?" || qs[0].Client != "claude-code" {
		t.Fatalf("entries: %+v", qs)
	}
	if len([]rune(qs[1].Question)) != MaxQuestion {
		t.Errorf("not capped: %d runes", len([]rune(qs[1].Question)))
	}
	if !dismissed["abc"] {
		t.Error("dismissal lost")
	}
	if st, _ := os.Stat(l.Path()); st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("log is readable by others: %v", st.Mode())
	}
}

func TestMissingLogIsEmpty(t *testing.T) {
	qs, d, err := Open(filepath.Join(t.TempDir(), "none.jsonl")).Read()
	if err != nil || len(qs) != 0 || d == nil {
		t.Fatalf("%v %v %v", qs, d, err)
	}
}

func TestGroupQuestions(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	g := GroupQuestions([]Entry{
		{At: at(0), Question: "How do we rotate the Jira token?", Client: "claude-code"},
		{At: at(1), Question: "what is the release train", Client: "claude-code"},
		{At: at(2), Question: "how do we rotate the jira token", Client: "cursor"},
		{At: at(3), Question: "how to rotate the jira token", Client: "claude-code"},
		{At: at(4), Question: "how do we rotate the jira token?", Client: "claude-code"},
	}, sim, norm)
	if len(g) != 2 {
		t.Fatalf("groups: %d", len(g))
	}
	a := g[0]
	if a.Asked != 4 || len(a.Phrasings) != 2 || a.Counts[0] != 3 || a.Phrasings[0] != "How do we rotate the Jira token?" {
		t.Errorf("group: %+v", a)
	}
	if !a.First.Equal(at(0)) || !a.Last.Equal(at(4)) || strings.Join(a.Clients, ",") != "claude-code,cursor" {
		t.Errorf("times/clients: %+v", a)
	}
	again := GroupQuestions([]Entry{{At: at(9), Question: "how do we rotate the jira token"}}, sim, norm)
	if again[0].ID != a.ID {
		t.Error("ID is not stable for the same first phrasing")
	}
}

func TestConcurrentAppendAndRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gaps.jsonl")
	// Two processes are two Logs on one file.
	a, b := Open(path), Open(path)
	q := strings.Repeat("x", 400)
	var wg sync.WaitGroup
	for _, l := range []*Log{a, b} {
		wg.Add(1)
		go func(l *Log) {
			defer wg.Done()
			for i := 0; i < 1500; i++ {
				if err := l.Record(q, "c", time.Now()); err != nil {
					t.Error(err)
					return
				}
			}
		}(l)
	}
	wg.Wait()
	// Under contention a rotation may be skipped; the next uncontended write does it.
	if err := a.Record(q, "c", time.Now()); err != nil {
		t.Fatal(err)
	}
	qs, _, err := a.Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range qs {
		if e.Question != q {
			t.Fatalf("torn line: %q", e.Question[:min(40, len(e.Question))])
		}
	}
	st, _ := os.Stat(path)
	if st.Size() > maxBytes+int64(len(q))*4 {
		t.Errorf("log not rotated: %d bytes", st.Size())
	}
	if len(qs) == 0 {
		t.Error("everything rotated away")
	}
}

func TestDefaultPathIsPerVault(t *testing.T) {
	a, err := DefaultPath("/vaults/a")
	if err != nil {
		t.Skip(err)
	}
	b, _ := DefaultPath("/vaults/b")
	if a == b || !strings.Contains(a, "okvault") || !strings.HasSuffix(a, ".jsonl") {
		t.Errorf("%s %s", a, b)
	}
}
