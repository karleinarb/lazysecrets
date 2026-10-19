package update

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBinaryReplaced(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "lazysecrets")
	write := func(version string) {
		t.Helper()
		tmp := filepath.Join(dir, ".new")
		if err := os.WriteFile(tmp, []byte("#!/bin/sh\necho lazysecrets "+version+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, exe); err != nil { // as Install and install.sh do
			t.Fatal(err)
		}
	}
	write("v0.3.0")
	info, _ := os.Stat(exe)
	b := &Binary{path: exe, info: info}
	ctx := context.Background()

	if v, ok := b.Replaced(ctx); ok {
		t.Errorf("the same file: replaced by %q", v)
	}
	time.Sleep(10 * time.Millisecond)
	write("v0.3.1")
	if v, ok := b.Replaced(ctx); !ok || v != "v0.3.1" {
		t.Errorf("installed over: %q %t", v, ok)
	}
	if v, ok := b.Replaced(ctx); ok {
		t.Errorf("said again: %q", v)
	}
}
