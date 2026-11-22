package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStdioSmoke builds the real binary, talks MCP over stdio, and checks that
// a file edited behind the server's back is picked up by the watcher.
func TestStdioSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "okvault")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	root := t.TempDir()
	note := filepath.Join(root, "faq", "q.md")
	_ = os.MkdirAll(filepath.Dir(note), 0o755)
	_ = os.WriteFile(note, []byte("---\ntype: FAQ\ntitle: What is okvault?\n---\nAn MCP server.\n"), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "--vault", root, "--author", "smoke")
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	ask := func(q string) map[string]any {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "kb_ask", Arguments: map[string]any{"question": q}})
		if err != nil || res.IsError {
			t.Fatalf("kb_ask: %v %+v", err, res)
		}
		var m map[string]any
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &m)
		return m
	}
	if a := ask("what is okvault"); a["answer"] != "An MCP server." {
		t.Fatalf("ask = %v", a)
	}
	// edit on disk, as Obsidian or git pull would
	_ = os.WriteFile(note, []byte("---\ntype: FAQ\ntitle: What is okvault?\n---\nAn OKF knowledge server.\n"), 0o644)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a := ask("what is okvault"); a["answer"] == "An OKF knowledge server." {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("watcher did not pick up the edit")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
