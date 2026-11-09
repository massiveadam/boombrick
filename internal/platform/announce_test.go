package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAnnounce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plorpos-music-playing")
	var playing atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Announce(ctx, path, 5*time.Millisecond, playing.Load); close(done) }()
	read := func() string { b, _ := os.ReadFile(path); return strings.TrimSpace(string(b)) }
	me := strconv.Itoa(os.Getpid())

	waitFor(t, "paused", func() bool { return read() == me+" paused" })
	playing.Store(true)
	waitFor(t, "playing", func() bool { return read() == me+" playing" })
	playing.Store(false)
	waitFor(t, "paused again", func() bool { return read() == me+" paused" })

	// Paused, it leaves a live player's claim alone (pid 1 is always there).
	if err := os.WriteFile(path, []byte("1 playing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if read() != "1 playing" {
		t.Fatalf("a paused player took a playing one's file: %q", read())
	}
	// Playing, it takes the file back.
	playing.Store(true)
	waitFor(t, "the claim back", func() bool { return read() == me+" playing" })

	// From a player that has gone, even paused.
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	playing.Store(false)
	if err := os.WriteFile(path, []byte(strconv.Itoa(dead.Process.Pid)+" playing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a dead player's file taken", func() bool { return read() == me+" paused" })

	// Ending, it removes only its own file.
	if err := os.WriteFile(path, []byte("1 paused\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if read() != "1 paused" {
		t.Fatalf("removed another player's file: %q", read())
	}
}

func TestAnnounceRemovesOwn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plorpos-music-playing")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { Announce(ctx, path, 5*time.Millisecond, func() bool { return true }); close(done) }()
	waitFor(t, "the file", func() bool { _, err := os.Stat(path); return err == nil })
	cancel()
	<-done
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("left its file behind")
	}
}

func TestCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plorpos-music-command")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmds := Commands(ctx, path)
	if err := os.WriteFile(path, []byte("next\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // Notify is in place before the goroutine reads
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-cmds:
		if c != "next" {
			t.Fatalf("got %q", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no command")
	}
}

func TestPlayingFileOnlyRGSP(t *testing.T) {
	if (Profile{Firmware: NextUI, tmp: "/tmp"}).PlayingFile() != "" || (Profile{Firmware: NextUI, tmp: "/tmp"}).CommandFile() != "" {
		t.Fatal("the Brick announces")
	}
	if got := (Profile{Firmware: RGSP, tmp: "/tmp"}).PlayingFile(); got != "/tmp/plorpos-music-playing" {
		t.Fatalf("RG SP file %q", got)
	}
}
