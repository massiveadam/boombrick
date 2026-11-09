package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRescanAfterSync(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(state, []byte("a"), 0o644)
	got := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go rescanAfterSync(ctx, state, 20*time.Millisecond, func() { got <- struct{}{} })
	select {
	case <-got:
		t.Fatal("rescanned with nothing synced")
	case <-time.After(100 * time.Millisecond):
	}
	old := time.Now().Add(-time.Second) // a run done, the file still since
	os.WriteFile(state, []byte("b"), 0o644)
	os.Chtimes(state, old, old)
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("no rescan after the sync changed the library")
	}
	select {
	case <-got:
		t.Fatal("rescanned twice for one change")
	case <-time.After(100 * time.Millisecond):
	}
}
