package search

import (
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
