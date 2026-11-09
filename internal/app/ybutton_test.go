package app

import (
	"slices"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/platform/input"
)

// A tap of Y is the screen's, heard as it is let go; held for yHold it opens
// the Queue instead, and the screen hears nothing.
func TestYTapAndHold(t *testing.T) {
	a, tab := darkApp(nil)
	q := &page{name: "queue"}
	a.queueAt = a.shell.add(overlay{button: input.None, screen: q})
	a.dispatch(press(input.Y))
	if len(tab.got) != 0 {
		t.Fatalf("Y reached the screen while still down: %v", tab.got)
	}
	a.dispatch(release(input.Y))
	if !slices.Equal(tab.got, []input.Button{input.Y, input.Y}) {
		t.Fatalf("a tap: the screen heard %v", tab.got)
	}

	tab.got = nil
	a.dispatch(press(input.Y))
	a.yHeld(time.Now().Add(yHold / 2))
	if a.shell.open >= 0 {
		t.Fatal("the Queue opened before Y was held long enough")
	}
	a.yHeld(time.Now().Add(yHold))
	a.dispatch(release(input.Y))
	if a.shell.open != a.queueAt || len(tab.got) != 0 {
		t.Fatalf("a hold: open %d, the screen heard %v", a.shell.open, tab.got)
	}
}

// Select with nothing from the library playing says so, and opens nothing.
func TestSelectWithNothingPlaying(t *testing.T) {
	a, tab := darkApp(nil)
	a.shell.add(overlay{button: input.Select, act: a.openPlayingAlbum})
	a.dispatch(press(input.Select))
	if len(a.shell.stacks[0]) != 1 || len(tab.got) != 0 || a.shell.toast == "" {
		t.Fatalf("pages %d, heard %v, toast %q", len(a.shell.stacks[0]), tab.got, a.shell.toast)
	}
}
