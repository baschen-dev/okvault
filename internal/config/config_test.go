package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseAndTicketKeys(t *testing.T) {
	c, err := Parse([]byte("tracker:\n  kind: jira\n  url: https://acme.atlassian.net/\n  projects: [proj, OPS]\nowners:\n  ops: [alice]\n  ops/db: [bob]\n  /: [team]\nreview_interval: 90d\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Tracker.API != "3" || c.Tracker.URL != "https://acme.atlassian.net" {
		t.Fatalf("tracker defaults: %+v", c.Tracker)
	}
	got := c.TicketKeys("Fixes PROJ-12 and OPS-3; not UTF-8, ISO-8601, XPROJ-1 or PROJ-0.")
	if strings.Join(got, ",") != "PROJ-12,OPS-3" {
		t.Fatalf("keys: %v", got)
	}
	if c.Review() != 90*24*time.Hour {
		t.Fatalf("review %v", c.Review())
	}
	if o := c.OwnersFor("ops/db/backup.md"); len(o) != 1 || o[0] != "bob" {
		t.Fatalf("owners %v", o)
	}
	if o := c.OwnersFor("ops/deploy.md"); o[0] != "alice" {
		t.Fatalf("owners %v", o)
	}
	if o := c.OwnersFor("faq/x.md"); o[0] != "team" {
		t.Fatalf("owners %v", o)
	}
	if u := c.BrowseURL("PROJ-12"); u != "https://acme.atlassian.net/browse/PROJ-12" {
		t.Fatal(u)
	}
}

func TestParseRejects(t *testing.T) {
	for _, src := range []string{
		"tracker:\n  kind: jira\n  url: https://x.atlassian.net\n  token: abc\n",
		"tracker:\n  kind: jira\n  url: https://me:secret@x.atlassian.net\n",
		"tracker:\n  kind: jira\n",
		"tracker:\n  kind: trello\n",
		"tracker:\n  projects: [not a key]\n",
		"review_interval: soon\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("want error for %q", src)
		}
	}
	if _, err := Parse([]byte("# only comments\n")); err != nil {
		t.Fatalf("comment-only config: %v", err)
	}
	c, err := Parse(nil)
	if err != nil || c.TicketKeys("PROJ-1") != nil || c.BrowseURL("PROJ-1") != "" {
		t.Fatalf("empty config: %v %+v", err, c)
	}
}

func TestNormalizeKey(t *testing.T) {
	if k, ok := NormalizeKey(" proj-42 "); !ok || k != "PROJ-42" {
		t.Fatal(k, ok)
	}
	for _, bad := range []string{"proj", "-1", "PROJ-", "PROJ-x", "a b-1", "PROJ-0"} {
		if _, ok := NormalizeKey(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGitConfig(t *testing.T) {
	c, err := Parse([]byte("git:\n  mode: PR\n  base: main\n  pull_interval: 2m\n  commit_edits: 10m\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := c.Git
	if g.Mode != GitPR || g.Remote != "origin" || g.Every() != 2*time.Minute || g.Quiet() != 10*time.Minute {
		t.Fatalf("git = %+v every=%v quiet=%v", g, g.Every(), g.Quiet())
	}
	c, _ = Parse(nil)
	if c.Git.Mode != GitOff || c.Git.Every() != DefaultPullInterval || c.Git.Quiet() != 0 {
		t.Fatalf("defaults = %+v", c.Git)
	}
	if c, _ := Parse([]byte("git:\n  mode: push\n  pull_interval: \"0\"\n")); c.Git.Every() != 0 {
		t.Fatal("pull_interval 0 should turn periodic sync off")
	}
	for _, bad := range []string{
		"git:\n  mode: sometimes\n",
		"git:\n  remote: https://github.com/o/r\n",
		"git:\n  branch: \"a..b\"\n",
		"git:\n  token: x\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
