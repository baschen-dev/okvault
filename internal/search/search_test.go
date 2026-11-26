package search

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/baschen-dev/okvault/internal/vault"
)

func build(t *testing.T, files map[string]string) *Index {
	t.Helper()
	root := t.TempDir()
	for rel, c := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := Build(v.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

var corpus = map[string]string{
	"faq/release.md": "---\ntype: FAQ\ntitle: How do we release a new version?\naliases: [How to cut a release, Publishing a release]\ntags: [ops]\n---\nTag main with vX.Y.Z and push; CI publishes.\n",
	"faq/vpn.md":     "---\ntype: FAQ\ntitle: How do I connect to the VPN?\n---\nUse WireGuard with the profile from the wiki.\n",
	"ops/deploy.md":  "---\ntype: Playbook\ntitle: Deploying the API\ndescription: Steps to deploy the API to production.\ntags: [ops, api]\n---\nRun just deploy. Rollback with just rollback.\n",
	"ops/old.md":     "---\ntype: Playbook\ntitle: Old deploy\nstatus: deprecated\n---\nDeploy by FTP.\n",
	"ref/go.md":      "---\ntype: Reference\ntitle: Go modules\ntags: [go]\n---\nUse go mod tidy before committing.\n",
}

func TestAskConfident(t *testing.T) {
	ix := build(t, corpus)
	for _, q := range []string{"How do we release a new version?", "how to cut a release", "releasing a new version", "connect VPN"} {
		a, err := ix.Ask(q)
		if err != nil {
			t.Fatal(err)
		}
		if !a.Confident {
			t.Errorf("%q: want confident, got %+v", q, a)
		}
	}
	a, _ := ix.Ask("how to cut a release")
	if a.Path != "faq/release.md" || a.Answer == "" {
		t.Errorf("wrong answer %+v", a)
	}
}

func TestAskFallsBackToHits(t *testing.T) {
	ix := build(t, corpus)
	a, err := ix.Ask("what is the rollback procedure for the API?")
	if err != nil {
		t.Fatal(err)
	}
	if a.Confident {
		t.Fatalf("should not be confident: %+v", a)
	}
	if len(a.Related) == 0 || a.Related[0].Path != "ops/deploy.md" {
		t.Fatalf("want ops/deploy.md first, got %+v", a.Related)
	}
}

func TestSearchRankingAndFilters(t *testing.T) {
	ix := build(t, corpus)
	hits, err := ix.Search(Query{Text: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "ops/deploy.md" {
		t.Fatalf("deprecated note should be hidden: %+v", hits)
	}
	hits, _ = ix.Search(Query{Text: "deploy", WithDeprecated: true})
	if len(hits) != 2 {
		t.Fatalf("want 2 with deprecated, got %+v", hits)
	}
	hits, _ = ix.Search(Query{Tags: []string{"ops"}, Type: "faq"})
	if len(hits) != 1 || hits[0].Path != "faq/release.md" {
		t.Fatalf("filter: %+v", hits)
	}
	hits, _ = ix.Search(Query{Dir: "ref"})
	if len(hits) != 1 || hits[0].Path != "ref/go.md" {
		t.Fatalf("dir filter: %+v", hits)
	}
	hits, _ = ix.Search(Query{Text: "modles"}) // typo
	if len(hits) == 0 || hits[0].Path != "ref/go.md" {
		t.Fatalf("fuzzy: %+v", hits)
	}
}

func TestSnippet(t *testing.T) {
	body := "# Title\n\nIntro line.\nRollback with just rollback.\n"
	if got := Snippet(body, "rollback", 100); got != "Rollback with just rollback." {
		t.Fatalf("got %q", got)
	}
}

func TestAskRejectsPartialMatches(t *testing.T) {
	ix := build(t, map[string]string{
		"faq/what.md":    "---\ntype: FAQ\ntitle: What is okvault?\n---\nAn MCP server.\n",
		"faq/release.md": corpus["faq/release.md"],
	})
	for _, q := range []string{
		"what is the okvault license?",
		"how do I install okvault on windows",
		"release notes format",
	} {
		a, err := ix.Ask(q)
		if err != nil {
			t.Fatal(err)
		}
		if a.Confident {
			t.Errorf("%q: must not be confident, matched %q (%.2f)", q, a.Question, a.Confidence)
		}
	}
	if a, _ := ix.Ask("what's okvault"); !a.Confident || a.Path != "faq/what.md" {
		t.Errorf("short exact-ish question should match: %+v", a)
	}
}

func TestAskSkipsDraftAndDeprecatedFAQs(t *testing.T) {
	ix := build(t, map[string]string{
		"faq/token.md":  "---\ntype: FAQ\ntitle: How do we rotate the Jira token?\nstatus: draft\n---\nAnswer in a few lines.\n",
		"faq/old.md":    "---\ntype: FAQ\ntitle: How do we deploy to the old cluster?\nstatus: deprecated\n---\nDo not.\n",
		"faq/stable.md": "---\ntype: FAQ\ntitle: How do we release?\n---\nTag and push.\n",
	})
	a, err := ix.Ask("How do we rotate the Jira token?")
	if err != nil {
		t.Fatal(err)
	}
	if a.Confident || a.Answer != "" {
		t.Fatalf("a draft answered: %+v", a)
	}
	if a.Path != "faq/token.md" || a.Confidence < 0.99 {
		t.Errorf("the draft should still be the nearest FAQ: %+v", a)
	}
	if a, _ := ix.Ask("How do we deploy to the old cluster?"); a.Confident || a.Path == "faq/old.md" {
		t.Errorf("a deprecated FAQ matched: %+v", a)
	}
	if a, _ := ix.Ask("how do we release"); !a.Confident {
		t.Errorf("stable FAQ: %+v", a)
	}
}

func note(title, body string, extra ...string) string {
	fm := "---\ntype: Reference\ntitle: " + title + "\n"
	for _, e := range extra {
		fm += e + "\n"
	}
	return fm + "---\n" + body + "\n"
}

func TestSearchPrefersLinkedNotes(t *testing.T) {
	files := map[string]string{
		"a.md":   note("Cache settings", "Tune the cache size."),
		"z.md":   note("Cache settings", "Tune the cache size."),
		"old.md": note("Old", "[see](a.md)", "status: deprecated"),
	}
	for _, n := range []string{"l1", "l2", "l3"} {
		files[n+".md"] = note("Linker "+n, "[see](z.md)")
	}
	// z.md links to itself too, which must not count
	files["z.md"] = note("Cache settings", "Tune the cache size. [self](z.md)")
	files["a.md"] = note("Cache settings", "Tune the cache size. [self](a.md)")
	ix := build(t, files)
	hits, err := ix.Search(Query{Text: "cache"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].Path != "z.md" || hits[1].Path != "a.md" {
		t.Fatalf("want the linked note first: %+v", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("score should follow the order: %+v", hits)
	}
}

func TestSearchLinksDoNotOvertakeBetterMatch(t *testing.T) {
	files := map[string]string{
		"strong.md": note("Rollback procedure", "How to roll back: rollback the release."),
		"hub.md":    note("Overview", "Everything about the service, its parts, owners, alerts and a rollback."),
	}
	for i := 0; i < 40; i++ {
		files[fmt.Sprintf("n/%02d.md", i)] = note(fmt.Sprintf("Part %d", i), "[hub](../hub.md)")
	}
	ix := build(t, files)
	hits, err := ix.Search(Query{Text: "rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].Path != "strong.md" {
		t.Fatalf("a hub must not beat a clearly better match: %+v", hits)
	}
}

func TestSearchNearTicket(t *testing.T) {
	files := map[string]string{
		"seed.md":  note("Ticket work", "Notes for the ticket. [next](near.md)", "tickets: [PROJ-1]"),
		"near.md":  note("Queue tuning", "Set the queue depth."),
		"far.md":   note("Queue tuning", "Set the queue depth."),
		"other.md": note("Unrelated", "[far](far.md) [near](near.md)"),
	}
	ix := build(t, files)
	hits, err := ix.Search(Query{Text: "queue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].Path != "near.md" {
		// near.md has two backlinks, far.md one: the link prior alone orders them
		t.Fatalf("without a ticket: %+v", hits)
	}
	files["seed.md"] = note("Ticket work", "Notes for the ticket. [next](far.md)", "tickets: [PROJ-1]")
	files["other.md"] = note("Unrelated", "[near](near.md)")
	ix = build(t, files)
	plain, _ := ix.Search(Query{Text: "queue"})
	withKey, err := ix.Search(Query{Text: "queue", NearTickets: []string{"PROJ-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(withKey) < 2 || withKey[0].Path != "far.md" || withKey[0].Score <= plain[0].Score {
		t.Fatalf("the note next to PROJ-1 should rank first: plain %+v, with key %+v", plain, withKey)
	}
	inText, _ := ix.Search(Query{Text: "queue PROJ-1"})
	score := map[string]float64{}
	for _, h := range inText {
		score[h.Path] = h.Score
	}
	if score["far.md"] <= score["near.md"] {
		t.Fatalf("a ticket key in the text should favour its neighbours: %+v", inText)
	}
}
