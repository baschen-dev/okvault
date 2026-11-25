package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/baschen-dev/okvault/internal/server"
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

// TestWatcherRegeneratesIndex checks that a note created on disk (as in
// Obsidian) shows up in its folder's generated index.md while serving.
func TestWatcherRegeneratesIndex(t *testing.T) {
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
	_ = os.MkdirAll(filepath.Join(root, "faq"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "faq", "a.md"), []byte("---\ntype: FAQ\ntitle: A?\n---\na\n"), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "serve", "--vault", root)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	_ = os.WriteFile(filepath.Join(root, "faq", "b.md"), []byte("---\ntype: FAQ\ntitle: Brand new?\ndescription: Added in Obsidian.\n---\nb\n"), 0o644)
	deadline := time.Now().Add(5 * time.Second)
	for {
		idx, _ := os.ReadFile(filepath.Join(root, "faq", "index.md"))
		if strings.Contains(string(idx), "[Brand new?](./b.md) - Added in Obsidian.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("faq/index.md not regenerated:\n%s", idx)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// a note mentions ZED-1 before ZED is a configured project: no mention yet
	_ = os.WriteFile(filepath.Join(root, "faq", "c.md"), []byte("---\ntype: FAQ\ntitle: C?\n---\nTracked in ZED-1.\n"), 0o644)
	deadline = time.Now().Add(5 * time.Second)
	for {
		idx, _ := os.ReadFile(filepath.Join(root, "faq", "index.md"))
		if strings.Contains(string(idx), "](./c.md)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("c.md not loaded:\n%s", idx)
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second) // let the reload caused by the index rewrite settle
	if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "kb_ticket", Arguments: map[string]any{"key": "ZED-1"}}); err != nil {
		t.Fatal(err)
	} else if b, _ := json.Marshal(res.StructuredContent); strings.Contains(string(b), "faq/c.md") {
		t.Fatalf("ZED is not configured yet: %s", b)
	}
	// editing only the team config while serving makes the key count
	_ = os.WriteFile(filepath.Join(root, ".okvault.yaml"), []byte("tracker:\n  projects: [ZED]\n"), 0o644)
	deadline = time.Now().Add(5 * time.Second)
	for {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "kb_ticket", Arguments: map[string]any{"key": "ZED-1"}})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res.StructuredContent)
		if strings.Contains(string(b), `"path":"faq/c.md"`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("config change not picked up: %s", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestHTTPServe talks MCP over Streamable HTTP to the /mcp endpoint.
func TestHTTPServe(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "faq"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "faq", "q.md"), []byte("---\ntype: FAQ\ntitle: What is okvault?\n---\nAn MCP server.\n"), 0o644)
	v, ix, err := open(nil, root)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(v, ix, server.Options{ReadOnly: true, Version: "test", Env: func(string) string { return "" }})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	served := make(chan error, 1)
	sctx, stop := context.WithCancel(ctx)
	go func() { served <- serveListener(sctx, srv.MCP, ln, root, true) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "http", Version: "0"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: "http://" + ln.Addr().String() + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "kb_ask", Arguments: map[string]any{"question": "what is okvault"}})
	if err != nil || res.IsError {
		t.Fatalf("kb_ask: %v %+v", err, res)
	}
	if b, _ := json.Marshal(res.StructuredContent); !strings.Contains(string(b), "An MCP server.") {
		t.Fatalf("kb_ask = %s", b)
	}
	_ = cs.Close()

	stop()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

// TestLintConfigStdin: lint --config - checks a draft .okvault.yaml from stdin,
// in lint's own output format, without a vault.
func TestLintConfigStdin(t *testing.T) {
	for _, c := range []struct {
		name, src, want string
		fail            bool
	}{
		{"valid", "git:\n  mode: push\nreview_interval: 30d\n", "0 errors", false},
		{"empty", "# only a comment\n", "0 errors", false},
		{"unknown key", "token: secret\n", "error   .okvault.yaml: yaml: unmarshal errors:", true},
		{"bad interval", "review_interval: soon\n", "error   .okvault.yaml: review_interval:", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			err := lint([]string{"--config", "-"}, strings.NewReader(c.src), &out)
			if (err != nil) != c.fail {
				t.Fatalf("err = %v, want failure %v; output:\n%s", err, c.fail, out.String())
			}
			if !strings.Contains(out.String(), c.want) {
				t.Fatalf("output %q does not contain %q", out.String(), c.want)
			}
		})
	}
}
