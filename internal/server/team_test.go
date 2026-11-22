package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/vault"
)

func connectTeam(t *testing.T, files map[string]string, env map[string]string) (*mcp.ClientSession, *vault.Vault) {
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
	ix, err := search.Build(v.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(v, ix, Options{Author: "claude-code", Env: func(k string) string { return env[k] }})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.MCP.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, v
}

func teamFiles(jiraURL string) map[string]string {
	return map[string]string{
		".okvault.yaml": "tracker:\n  kind: jira\n  url: " + jiraURL + "\n  api: \"3\"\n  projects: [PROJ]\nowners:\n  ops: [alice]\n",
		"ops/db-password.md": "---\ntype: Playbook\ntitle: Rotating the database password\ndescription: How we rotate database credentials.\ntickets: [PROJ-7]\n---\n" +
			"Run `just rotate-db` on staging, then prod.\n",
		"ops/incident.md": "---\ntype: Report\ntitle: November outage\ndescription: What broke.\n---\nFollow-up in PROJ-7. Not a ticket: `PROJ-99`.\n",
		"ref/secrets.md":  "---\ntype: Reference\ntitle: Secrets storage\ndescription: Where database passwords and API keys live.\n---\nAll secrets are in the vault.\n",
	}
}

func TestTicketContext(t *testing.T) {
	jira := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/issue/PROJ-7" || r.Header.Get("Authorization") != "Bearer pat" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"key":"PROJ-7","fields":{"summary":"Rotate the database password","labels":["secrets"],"status":{"name":"In Progress"}}}`))
	}))
	defer jira.Close()
	cs, _ := connectTeam(t, teamFiles(jira.URL), map[string]string{"JIRA_API_TOKEN": "pat"})

	var out TicketOut
	call(t, cs, "kb_ticket", map[string]any{"key": "proj-7"}, &out)
	if out.Ticket == nil || out.Ticket.Status != "In Progress" || out.TrackerNote != "" {
		t.Fatalf("ticket not fetched: %+v", out)
	}
	if out.URL != jira.URL+"/browse/PROJ-7" {
		t.Fatalf("url %q", out.URL)
	}
	if len(out.Linked) != 1 || out.Linked[0].Path != "ops/db-password.md" {
		t.Fatalf("linked: %+v", out.Linked)
	}
	if len(out.Mentioned) != 1 || out.Mentioned[0].Path != "ops/incident.md" {
		t.Fatalf("mentioned: %+v", out.Mentioned)
	}
	if len(out.Suggested) == 0 || out.Suggested[0].Path != "ref/secrets.md" {
		t.Fatalf("suggested: %+v", out.Suggested)
	}

	// key inside inline code is not a mention; unknown ticket still answers
	var none TicketOut
	call(t, cs, "kb_ticket", map[string]any{"key": "PROJ-99"}, &none)
	if len(none.Linked)+len(none.Mentioned) != 0 || none.TrackerNote == "" || !strings.Contains(none.Hint, "No note references PROJ-99") {
		t.Fatalf("PROJ-99: %+v", none)
	}

	var so SearchOut
	call(t, cs, "kb_search", map[string]any{"query": "PROJ-7"}, &so)
	if len(so.Hits) < 2 {
		t.Fatalf("search by key: %+v", so.Hits)
	}
	call(t, cs, "kb_search", map[string]any{"ticket": "PROJ-7", "owner": "alice"}, &so)
	if len(so.Hits) != 2 {
		t.Fatalf("ticket+owner filter: %+v", so.Hits)
	}
}

func TestTicketWithoutCredentials(t *testing.T) {
	cs, _ := connectTeam(t, teamFiles("https://acme.atlassian.net"), nil)
	var out TicketOut
	res := call(t, cs, "kb_ticket", map[string]any{"key": "PROJ-7"}, &out)
	if res.IsError || out.Ticket != nil || !strings.Contains(out.TrackerNote, "JIRA_API_TOKEN") || len(out.Linked) != 1 {
		t.Fatalf("want notes without tracker: %+v", out)
	}
	if out.URL != "https://acme.atlassian.net/browse/PROJ-7" {
		t.Fatalf("url %q", out.URL)
	}
	if res := call(t, cs, "kb_ticket", map[string]any{"key": "nope"}, nil); !res.IsError {
		t.Fatal("bad key should be a tool error")
	}
}

func TestRevisionConflictAndReview(t *testing.T) {
	cs, v := connectTeam(t, teamFiles("https://acme.atlassian.net"), nil)

	var g0 GetOut
	call(t, cs, "kb_get", map[string]any{"path": "ref/secrets.md"}, &g0)
	if g0.Revision == "" || len(g0.Owners) != 0 {
		t.Fatalf("get: %+v", g0)
	}
	var g GetOut
	call(t, cs, "kb_get", map[string]any{"path": "ops/db-password.md"}, &g)
	if len(g.Owners) != 1 || g.Owners[0] != "alice" || len(g.Tickets) != 1 || g.Tickets[0].Key != "PROJ-7" {
		t.Fatalf("owners/tickets: %+v", g)
	}
	rev := g.Revision

	// a teammate edits the file on disk (e.g. in Obsidian, or via git pull)
	p := filepath.Join(v.Root, "ops", "db-password.md")
	src, _ := os.ReadFile(p)
	_ = os.WriteFile(p, append(src, []byte("Ask in #ops first.\n")...), 0o644)
	if _, err := v.Reload(); err != nil {
		t.Fatal(err)
	}

	res := call(t, cs, "kb_write", map[string]any{"path": "ops/db-password.md", "body": "replaced", "base_revision": rev}, nil)
	if !res.IsError || !strings.Contains(text(res), "changed since revision") {
		t.Fatalf("stale write must conflict: %s", text(res))
	}
	call(t, cs, "kb_get", map[string]any{"path": "ops/db-password.md"}, &g)
	var wr vault.WriteResult
	res = call(t, cs, "kb_write", map[string]any{
		"path": "ops/db-password.md", "body": g.Body + "Prod needs a change window.\n",
		"base_revision": g.Revision, "tickets": []string{"PROJ-7", "proj-8"},
	}, &wr)
	if res.IsError || wr.Revision == "" || wr.Revision == g.Revision {
		t.Fatalf("write with fresh revision: %s %+v", text(res), wr)
	}
	call(t, cs, "kb_get", map[string]any{"path": "ops/db-password.md"}, &g)
	if !strings.Contains(g.Body, "#ops") || len(g.Tickets) != 2 || g.Tickets[1].Key != "PROJ-8" {
		t.Fatalf("merged write: %+v", g)
	}

	var rv ReviewOut
	call(t, cs, "kb_review", map[string]any{"reason": "unverified"}, &rv)
	if rv.Total != 1 || rv.Items[0].Path != "ops/db-password.md" || rv.Items[0].Generated.By != "claude-code" {
		t.Fatalf("review: %+v", rv)
	}
	call(t, cs, "kb_review", map[string]any{"reason": "no-owner"}, &rv)
	if rv.Total != 1 || rv.Items[0].Path != "ref/secrets.md" {
		t.Fatalf("no-owner: %+v", rv)
	}

	// a person checks it
	if _, err := v.Verify("ops/db-password.md", "Alice", time.Now()); err != nil {
		t.Fatal(err)
	}
	call(t, cs, "kb_review", map[string]any{"reason": "unverified"}, &rv)
	if rv.Total != 0 {
		t.Fatalf("verified note still listed: %+v", rv)
	}
	c := v.Snapshot().Concepts["ops/db-password.md"]
	if lv, ok := c.LastVerified(); !ok || lv.By != "Alice" {
		t.Fatalf("verified: %+v", c.Meta())
	}
	if sa, _, ok := c.StaleAfter(); !ok || sa.Before(time.Now().Add(170*24*time.Hour)) {
		t.Fatalf("stale_after not pushed: %v", sa)
	}
	if items := v.Snapshot().Review(vault.ReviewQuery{Reason: "stale"}, time.Now().Add(200*24*time.Hour)); len(items) != 1 {
		t.Fatalf("stale after interval: %+v", items)
	}
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
