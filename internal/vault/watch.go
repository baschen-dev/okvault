package vault

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch reloads the vault when Markdown files change (debounced) and calls
// onReload with the new snapshot. It blocks until ctx is done.
func (v *Vault) Watch(ctx context.Context, debounce time.Duration, onReload func(*Snapshot)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	addTree := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if p != v.Root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			_ = w.Add(p)
			return nil
		})
	}
	addTree(v.Root)

	var timer *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Has(fsnotify.Create) {
				if fi, err := statDir(ev.Name); err == nil && fi {
					addTree(ev.Name)
				}
			}
			if !relevant(v.Root, ev.Name) {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
		case <-fire:
			snap, err := v.Reload()
			if err != nil {
				slog.Warn("reload failed", "err", err)
				continue
			}
			slog.Debug("vault reloaded", "concepts", len(snap.Concepts))
			if onReload != nil {
				onReload(snap)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Warn("watch error", "err", err)
		}
	}
}

func relevant(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_") {
			return false
		}
	}
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".md" || ext == "" // "" = directory rename/delete
}
