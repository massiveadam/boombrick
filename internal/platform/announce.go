package platform

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PlayingFile is where the RG SP's plorpOS (patched to read it) learns about
// Boombrick's music: the running player's process id and "playing" or
// "paused". Playing, plorpOS's sleep turns only the screen off, as for its own
// player, instead of muting and suspending the device; playing or paused, its
// volume-key music controls (Vol+ and Vol- held, both together) come here.
// Empty where nobody reads one.
func (p Profile) PlayingFile() string {
	if p.Firmware != RGSP {
		return ""
	}
	return filepath.Join(p.tmp, "plorpos-music-playing")
}

// CommandFile is where plorpOS leaves the music control it sends, "toggle",
// "next" or "prev", before it signals SIGUSR1. Empty where nothing sends one.
func (p Profile) CommandFile() string {
	if p.Firmware != RGSP {
		return ""
	}
	return filepath.Join(p.tmp, "plorpos-music-command")
}

// Announce keeps path saying, checked every interval, that this process's
// music plays or is paused, and removes it when ctx ends. Playing, it always
// claims the file. Paused, it claims it only from nobody, from a process that
// has gone, or from itself: the app and its background player, handing the
// music over, then settle on the one that plays. It removes only a file
// holding its own id. It returns once ctx ends. An empty path announces
// nothing.
func Announce(ctx context.Context, path string, every time.Duration, playing func() bool) {
	if path == "" {
		return
	}
	pid := os.Getpid()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		state := "paused"
		if playing() {
			state = "playing"
		}
		want := strconv.Itoa(pid) + " " + state + "\n"
		owner, got := readPlaying(path)
		if got != want && (state == "playing" || owner == 0 || owner == pid || !alive(owner)) {
			writeAtomic(path, want)
		}
		select {
		case <-ctx.Done():
			if owner, _ := readPlaying(path); owner == pid {
				os.Remove(path)
			}
			return
		case <-t.C:
		}
	}
}

// Commands sends each music control that arrives at path with a SIGUSR1,
// until ctx ends. An empty path sends none.
func Commands(ctx context.Context, path string) <-chan string {
	out := make(chan string, 4)
	if path == "" {
		return out
	}
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGUSR1)
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sig:
			}
			b, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			select {
			case out <- strings.TrimSpace(string(b)):
			default: // a burst of presses: the ones in hand are enough
			}
		}
	}()
	return out
}

// readPlaying returns the file's process id, 0 when there is none, and its
// contents.
func readPlaying(path string) (int, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, string(b)
	}
	pid, _ := strconv.Atoi(f[0])
	return pid, string(b)
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM // EPERM: there, another user's
}

func writeAtomic(path, s string) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(s), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
