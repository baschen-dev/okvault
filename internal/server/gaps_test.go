package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/gaps"
	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/vault"
)

// gapServer serves a one-FAQ vault with a gaps log; clients connect by name.
type gapServer struct {
	srv  *Server
	v    *vault.Vault
	log  *gaps.Log
	root string
}

func newGapServer(t *testing.T) *gapServer {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "faq"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "faq", "release.md"),
		[]byte("---\ntype: FAQ\ntitle: How do we release?\naliases: [cut a release]\n---\nTag and push.\n"), 0o644)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := search.Build(v.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	l := gaps.Open(filepath.Join(t.TempDir(), "gaps.jsonl"))
	return &gapServer{srv: New(v, ix, Options{Author: "test", Gaps: l}), v: v, log: l, root: root}
}

func (g *gapServer) client(t *testing.T, name string) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := g.srv.MCP.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: name, Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestKnowledgeGaps(t *testing.T) {
	g := newGapServer(t)
	agent := g.client(t, "claude-code")
	other := g.client(t, "cursor")
	plugin := g.client(t, PluginClient)
	ask := func(cs *mcp.ClientSession, q string) {
		t.Helper()
		var a search.Answer
		call(t, cs, "kb_ask", map[string]any{"question": q}, &a)
	}

	ask(agent, "how do we rotate the jira api token")
	ask(other, "How do we rotate the Jira API token?")
	ask(agent, "rotate the jira api token how")
	ask(agent, "how do we release a hotfix")    // near miss on the release FAQ
	ask(agent, "how to cut a release")          // answered: not recorded
	ask(agent, "what is the")                   // no content words: not recorded
	ask(plugin, "how do we rotate the jira ap") // a person typing in Obsidian: not recorded

	entries, _, err := g.log.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		var qs []string
		for _, e := range entries {
			qs = append(qs, e.Client+": "+e.Question)
		}
		t.Fatalf("recorded %d: %s", len(entries), strings.Join(qs, " | "))
	}

	var out GapsOut
	call(t, agent, "kb_gaps", map[string]any{}, &out)
	if out.Total != 2 || len(out.Gaps) != 2 {
		t.Fatalf("gaps: %+v", out)
	}
	top := out.Gaps[0]
	if top.Asked != 3 || !strings.Contains(strings.ToLower(top.Question), "rotate the jira api token") || len(top.Phrasings) != 2 { // case and punctuation do not make a new phrasing
		t.Errorf("top gap: %+v", top)
	}
	if strings.Join(top.Clients, ",") != "claude-code,cursor" {
		t.Errorf("clients: %v", top.Clients)
	}
	near := out.Gaps[1]
	if near.Nearest == nil || near.Nearest.Path != "faq/release.md" || near.Nearest.Confidence <= 0 {
		t.Errorf("near miss: %+v", near.Nearest)
	}

	// Writing the FAQ (with the other phrasings as aliases) answers the group.
	call(t, agent, "kb_write", map[string]any{
		"path": "faq/rotate-jira-token.md", "type": "FAQ", "title": "How do we rotate the Jira API token?",
		"aliases": []string{"rotate the jira api token how"}, "body": "In Jira, then update JIRA_API_TOKEN.",
	}, nil)
	g.srv.Refresh(g.v.Snapshot())
	call(t, agent, "kb_gaps", map[string]any{}, &out)
	if out.Total != 1 || out.Answered != 1 || out.Gaps[0].ID != near.ID {
		t.Fatalf("after the FAQ: %+v", out)
	}

	// A person dismisses the last one.
	call(t, agent, "kb_gaps", map[string]any{"dismiss": near.ID}, &out)
	if out.Total != 0 || out.Dismissed != 1 {
		t.Fatalf("after dismiss: %+v", out)
	}
}

func TestGapsOff(t *testing.T) {
	cs, _ := connect(t, false) // no Gaps log
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "kb_gaps" {
			t.Fatal("kb_gaps listed with gaps off")
		}
	}
	var a search.Answer
	call(t, cs, "kb_ask", map[string]any{"question": "something nobody wrote down"}, &a) // must not panic
}
