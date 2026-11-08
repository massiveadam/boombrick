package audio

import (
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Paused, the output is let go after the idle time, so a game can open a
// device that takes one program at a time; resuming opens it again.
func TestIdleReleaseAfterPause(t *testing.T) {
	out := &sink{}
	e := NewEngine(out, nil)
	defer e.Close()
	e.SetIdleRelease(50 * time.Millisecond)
	e.Play(&ramp{rate: 48000, n: 48000 * 60})
	waitFor(t, "playing", func() bool { return out.written() > 4800 })
	e.Pause()
	time.Sleep(20 * time.Millisecond)
	if out.isClosed() {
		t.Fatal("closed before the idle time")
	}
	waitFor(t, "release", out.isClosed)
	opens := out.openCount()
	e.Resume()
	waitFor(t, "reopen", func() bool { return out.openCount() > opens && !out.isClosed() })
}

// A pause shorter than the idle time keeps the output.
func TestIdleReleaseNotWhilePlaying(t *testing.T) {
	out := &sink{}
	e := NewEngine(out, nil)
	defer e.Close()
	e.SetIdleRelease(80 * time.Millisecond)
	e.Play(&ramp{rate: 48000, n: 48000 * 60})
	waitFor(t, "playing", func() bool { return out.written() > 4800 })
	e.Pause()
	time.Sleep(20 * time.Millisecond)
	e.Resume()
	time.Sleep(150 * time.Millisecond) // the first pause's timer has fired
	if out.isClosed() {
		t.Fatal("released while playing")
	}
}

// When the queue plays out, the output is let go too.
func TestIdleReleaseWhenStopped(t *testing.T) {
	out := &sink{}
	done := make(chan End, 1)
	e := NewEngine(out, func(x End) { done <- x })
	defer e.Close()
	e.SetIdleRelease(30 * time.Millisecond)
	e.Play(&ramp{rate: 48000, n: 4800})
	<-done
	waitFor(t, "release", out.isClosed)
}

// Without SetIdleRelease the output stays open, as upstream has it.
func TestNoIdleReleaseByDefault(t *testing.T) {
	out := &sink{}
	e := NewEngine(out, nil)
	defer e.Close()
	e.Play(&ramp{rate: 48000, n: 48000 * 60})
	waitFor(t, "playing", func() bool { return out.written() > 4800 })
	e.Pause()
	time.Sleep(100 * time.Millisecond)
	if out.isClosed() {
		t.Fatal("released without being asked to")
	}
}

// Stop lets go of the output too.
func TestIdleReleaseAfterStop(t *testing.T) {
	out := &sink{}
	e := NewEngine(out, nil)
	defer e.Close()
	e.SetIdleRelease(30 * time.Millisecond)
	e.Play(&ramp{rate: 48000, n: 48000 * 60})
	waitFor(t, "playing", func() bool { return out.written() > 4800 })
	e.Stop()
	waitFor(t, "release", out.isClosed)
}
