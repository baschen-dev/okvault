package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/vault"
)

func connect(t *testing.T, readOnly bool) (*mcp.ClientSession, *vault.Vault) {
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
	srv := New(v, ix, Options{ReadOnly: readOnly, Author: "test"})
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

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
	}
	return res
}

func TestToolsEndToEnd(t *testing.T) {
	cs, _ := connect(t, false)
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if got := strings.Join(names, ","); got != "kb_ask,kb_get,kb_list,kb_review,kb_search,kb_ticket,kb_write" {
		t.Fatalf("tools = %s", got)
	}

	var ans search.Answer
	call(t, cs, "kb_ask", map[string]any{"question": "how to cut a release"}, &ans)
	if !ans.Confident || ans.Answer != "Tag and push." {
		t.Fatalf("ask = %+v", ans)
	}

	var wr vault.WriteResult
	res := call(t, cs, "kb_write", map[string]any{
		"path": "ops/deploy.md", "type": "Playbook", "title": "Deploy",
		"description": "How to deploy.", "body": "Run [release](../faq/release.md) first, then just deploy.", "mode": "create",
	}, &wr)
	if res.IsError || !wr.Created {
		t.Fatalf("write: %+v %+v", res, wr)
	}

	var so SearchOut
	call(t, cs, "kb_search", map[string]any{"query": "deploy"}, &so)
	if len(so.Hits) == 0 || so.Hits[0].Path != "ops/deploy.md" {
		t.Fatalf("search after write = %+v", so)
	}

	var g GetOut
	call(t, cs, "kb_get", map[string]any{"path": "How do we release?"}, &g)
	if g.Path != "faq/release.md" || len(g.Backlinks) != 1 || g.Backlinks[0] != "ops/deploy.md" {
		t.Fatalf("get = %+v", g)
	}

	var l ListOut
	call(t, cs, "kb_list", map[string]any{"type": "playbook"}, &l)
	if l.Total != 2 || len(l.Notes) != 1 {
		t.Fatalf("list = %+v", l)
	}

	res = call(t, cs, "kb_write", map[string]any{"path": "../escape.md", "type": "X"}, nil)
	if !res.IsError {
		t.Fatal("path escape should be a tool error")
	}
	res = call(t, cs, "kb_get", map[string]any{"path": "nope"}, nil)
	if !res.IsError {
		t.Fatal("missing note should be a tool error")
	}
}

func TestReadOnlyHidesWrite(t *testing.T) {
	cs, _ := connect(t, true)
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "kb_write" {
			t.Fatal("kb_write exposed in read-only mode")
		}
	}
}

func TestReviewRescan(t *testing.T) {
	cs, v := connect(t, true)
	// A change the watcher never saw: there is no watcher in this test.
	if err := os.WriteFile(filepath.Join(v.Root, "faq", "broken.md"),
		[]byte("---\ntype: FAQ\ntitle: Broken\n---\nSee [gone](gone.md).\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out ReviewOut
	call(t, cs, "kb_review", map[string]any{"reason": "broken-links"}, &out)
	if out.Total != 0 {
		t.Fatalf("before rescan = %+v", out)
	}
	call(t, cs, "kb_review", map[string]any{"reason": "broken-links", "rescan": true}, &out)
	if out.Total != 1 || out.Items[0].Path != "faq/broken.md" {
		t.Fatalf("after rescan = %+v", out)
	}
	var so SearchOut
	call(t, cs, "kb_search", map[string]any{"query": "broken"}, &so)
	if len(so.Hits) == 0 || so.Hits[0].Path != "faq/broken.md" {
		t.Fatalf("search after rescan = %+v", so)
	}
}
