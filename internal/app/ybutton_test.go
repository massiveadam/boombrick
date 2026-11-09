package app

import (
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/screens"
	"github.com/ujjwal313/boombrick/internal/screens/browse"
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

// X and A together rescan, and neither reaches the screen; either alone
// reaches it once chordWait has passed, or at once when it is let go.
func TestXAndAChord(t *testing.T) {
	a, tab := darkApp(nil)
	a.chordWait = chordWait
	rescans := 0
	a.opts.Rescan = func() { rescans++ }
	a.dispatch(press(input.X))
	a.dispatch(press(input.A))
	a.dispatch(release(input.X))
	a.dispatch(release(input.A))
	a.chordHeld(time.Now().Add(time.Second))
	if rescans != 1 || len(tab.got) != 0 {
		t.Fatalf("together: rescans %d, the screen heard %v", rescans, tab.got)
	}

	a.dispatch(press(input.A))
	if len(tab.got) != 0 {
		t.Fatal("A reached the screen before the wait")
	}
	a.chordHeld(time.Now().Add(chordWait))
	a.dispatch(release(input.A))
	a.dispatch(press(input.X))
	a.dispatch(release(input.X)) // a quick tap
	if want := []input.Button{input.A, input.A, input.X, input.X}; !slices.Equal(tab.got, want) || rescans != 1 {
		t.Fatalf("alone: the screen heard %v, rescans %d", tab.got, rescans)
	}
}

// X twice opens the Library's search; once, it reaches the screen after the
// wait, press and release together.
func TestXTwiceSearches(t *testing.T) {
	a, tab := darkApp(nil)
	lib := &page{name: "library"}
	a.shell.stacks = append(a.shell.stacks, []screens.Screen{lib})
	a.chordWait, a.doubleWait = chordWait, doubleWait
	a.ix = library.NewIndex(nil)
	a.dispatch(press(input.X))
	a.dispatch(release(input.X))
	a.dispatch(press(input.X))
	a.dispatch(release(input.X))
	a.chordHeld(time.Now().Add(time.Second))
	if a.shell.tab != libraryTab || len(a.shell.stacks[libraryTab]) != 2 || len(tab.got) != 0 {
		t.Fatalf("tab %d, library pages %d, home heard %v", a.shell.tab, len(a.shell.stacks[libraryTab]), tab.got)
	}
	if _, ok := a.shell.top().(*browse.Search); !ok {
		t.Fatalf("on top: %T", a.shell.top())
	}

	a.shell.tab = 0
	a.dispatch(press(input.X))
	a.dispatch(release(input.X))
	if len(tab.got) != 0 {
		t.Fatal("X reached the screen before the wait")
	}
	a.chordHeld(time.Now().Add(doubleWait))
	if !slices.Equal(tab.got, []input.Button{input.X, input.X}) {
		t.Fatalf("one X: home heard %v", tab.got)
	}
}
