package gaps

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// holdExclusive opens path with no sharing, as Windows does to a file another
// okvault is renaming its rotated log over, and releases it after d.
func holdExclusive(t *testing.T, path string, d time.Duration) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(d)
		syscall.CloseHandle(h)
	}()
	t.Cleanup(func() { <-done }) // before TempDir's cleanup removes the file
}

func TestRecordAndReadWaitOutABusyLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gaps.jsonl")
	l := Open(path)
	if err := l.Record("first?", "c", time.Now()); err != nil {
		t.Fatal(err)
	}

	holdExclusive(t, path, 60*time.Millisecond)
	if err := l.Record("second?", "c", time.Now()); err != nil {
		t.Fatalf("record while the log was busy: %v", err)
	}
	holdExclusive(t, path, 60*time.Millisecond)
	qs, _, err := l.Read()
	if err != nil {
		t.Fatalf("read while the log was busy: %v", err)
	}
	if len(qs) != 2 {
		t.Fatalf("got %d questions, want 2", len(qs))
	}
}

func TestRecordGivesUpOnALogHeldForLong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gaps.jsonl")
	l := Open(path)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	holdExclusive(t, path, 3*time.Second)
	start := time.Now()
	if err := l.Record("q?", "c", time.Now()); err == nil {
		t.Fatal("recorded into a file held exclusively")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("waited %s; should give up within about 300ms", d)
	}
}
