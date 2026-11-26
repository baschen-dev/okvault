package server

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/baschen-dev/okvault/internal/search"
	"github.com/baschen-dev/okvault/internal/vault"
)

func refreshServer(t *testing.T) (*Server, *vault.Vault, *search.Index, string) {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "faq"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "faq", "a.md"), []byte("---\ntype: FAQ\ntitle: Alpha question?\n---\nalpha\n"), 0o644)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ix, err := search.Build(v.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	return New(v, ix, Options{ReadOnly: true}), v, ix, root
}

func found(t *testing.T, ix *search.Index, text, path string) bool {
	t.Helper()
	hits, err := ix.Search(search.Query{Text: text, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Path == path {
			return true
		}
	}
	return false
}

// The watcher, writes and rescan each reload and refresh; a refresh for an
// older snapshot that runs last must not put the older index back.
func TestLateRefreshKeepsTheNewestIndex(t *testing.T) {
	srv, v, ix, root := refreshServer(t)
	old := v.Snapshot()
	_ = os.WriteFile(filepath.Join(root, "faq", "b.md"), []byte("---\ntype: FAQ\ntitle: Bravo question?\n---\nbravo\n"), 0o644)
	snap, err := v.Reload()
	if err != nil {
		t.Fatal(err)
	}
	srv.Refresh(snap)
	srv.Refresh(old) // the slower of two overlapping refreshes
	if !found(t, ix, "bravo", "faq/b.md") {
		t.Fatal("a late refresh with an older snapshot dropped faq/b.md from the index")
	}
}

// Overlapping reloads (the watcher and a rescan, say) end with the vault and
// the index on what the disk holds.
func TestConcurrentReloadsEndOnTheDisk(t *testing.T) {
	srv, v, ix, root := refreshServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if snap, err := v.Reload(); err == nil {
				srv.Refresh(snap)
			}
		}()
	}
	_ = os.WriteFile(filepath.Join(root, "faq", "c.md"), []byte("---\ntype: FAQ\ntitle: Charlie question?\n---\ncharlie\n"), 0o644)
	// the reload that follows the write (as the watcher's would)
	snap, err := v.Reload()
	if err != nil {
		t.Fatal(err)
	}
	srv.Refresh(snap)
	wg.Wait()
	if v.Snapshot().Concepts["faq/c.md"] == nil {
		t.Fatal("an older reload replaced the snapshot that has faq/c.md")
	}
	if !found(t, ix, "charlie", "faq/c.md") {
		t.Fatal("an older refresh replaced the index that has faq/c.md")
	}
}
