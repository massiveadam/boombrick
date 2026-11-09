package app

import (
	"slices"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/ujjwal313/boombrick/internal/platform/input"
)

// A pad read as a game controller, as under Knulli, is heard by its
// mapping's names; SDL's joystick events for the same pad, numbered as
// NextUI never numbers them, are ignored. Another pad is still read by
// NextUI's numbers.
func TestControllersAreReadByTheirMapping(t *testing.T) {
	a, tab := darkApp(nil)
	a.pads = map[sdl.JoystickID]map[uint8]bool{7: {4: true, 5: true}}
	for _, ev := range []sdl.Event{
		&sdl.ControllerButtonEvent{Type: sdl.CONTROLLERBUTTONDOWN, Which: 7, Button: sdl.CONTROLLER_BUTTON_B, State: sdl.PRESSED},
		&sdl.ControllerButtonEvent{Type: sdl.CONTROLLERBUTTONUP, Which: 7, Button: sdl.CONTROLLER_BUTTON_B, State: sdl.RELEASED}, // not held: X with A rescans
		&sdl.JoyButtonEvent{Type: sdl.JOYBUTTONDOWN, Which: 7, Button: 5, State: sdl.PRESSED},
		&sdl.JoyHatEvent{Type: sdl.JOYHATMOTION, Which: 7, Value: sdl.HAT_UP},
		&sdl.ControllerAxisEvent{Type: sdl.CONTROLLERAXISMOTION, Which: 7, Axis: sdl.CONTROLLER_AXIS_TRIGGERLEFT, Value: 32767},
		&sdl.JoyButtonEvent{Type: sdl.JOYBUTTONDOWN, Which: 3, Button: 3, State: sdl.PRESSED},
	} {
		a.event(ev)
	}
	if want := []input.Button{input.A, input.A, input.L2, input.X}; !slices.Equal(tab.got, want) { // A down and up
		t.Fatalf("the screen heard %v, want %v", tab.got, want)
	}
}

// A mapping that names the face buttons by the Brick's labels is read so.
func TestLabelledControllers(t *testing.T) {
	a, tab := darkApp(nil)
	a.pads = map[sdl.JoystickID]map[uint8]bool{0: {}}
	a.mapper.Labelled = true
	a.event(&sdl.ControllerButtonEvent{Type: sdl.CONTROLLERBUTTONDOWN, Button: sdl.CONTROLLER_BUTTON_A, State: sdl.PRESSED})
	if !slices.Equal(tab.got, []input.Button{input.A}) {
		t.Fatalf("the screen heard %v", tab.got)
	}
}

// A controller's own joystick events leave a chord of Menu alone, but a
// button its mapping leaves out, as the volume keys may be, makes one, so
// Knulli's brightness, Menu with a volume key, doesn't open the quick menu.
func TestControllersAndChordsOfMenu(t *testing.T) {
	a, _ := darkApp(nil)
	m := &fakeMenu{}
	a.shell.menu = m
	a.pads = map[sdl.JoystickID]map[uint8]bool{0: {14: true}}
	guide := func(state uint8) *sdl.ControllerButtonEvent {
		typ := uint32(sdl.CONTROLLERBUTTONDOWN)
		if state == sdl.RELEASED {
			typ = sdl.CONTROLLERBUTTONUP
		}
		return &sdl.ControllerButtonEvent{Type: typ, Button: sdl.CONTROLLER_BUTTON_GUIDE, State: state}
	}
	a.event(guide(sdl.PRESSED))
	a.event(&sdl.JoyButtonEvent{Type: sdl.JOYBUTTONDOWN, Button: 14, State: sdl.PRESSED})
	a.event(guide(sdl.RELEASED))
	if !m.open {
		t.Fatal("Menu's own joystick event made a chord of it")
	}
	m.open = false
	a.event(guide(sdl.PRESSED))
	a.event(&sdl.JoyButtonEvent{Type: sdl.JOYBUTTONDOWN, Button: 20, State: sdl.PRESSED})
	a.event(guide(sdl.RELEASED))
	if m.open {
		t.Fatal("Menu with a button the mapping leaves out opened the quick menu")
	}
}

// With the quit chord, as under Knulli, where ports quit with Function and
// Start, Start pressed while Menu is held quits; Start first doesn't. It
// takes the chord on.
func TestMenuWithStartQuits(t *testing.T) {
	a, tab := darkApp(nil)
	a.dispatch(press(input.Menu))
	a.dispatch(press(input.Start))
	if a.quit {
		t.Fatal("quit without the chord")
	}
	a, tab = darkApp(nil)
	a.opts.QuitChord = true
	for _, e := range []input.Event{press(input.Start), press(input.Menu), release(input.Menu), release(input.Start)} {
		a.dispatch(e)
	}
	if a.quit {
		t.Fatal("Start, then Menu, quit")
	}
	tab.got = nil
	a.dispatch(press(input.Menu))
	a.dispatch(press(input.Start))
	if !a.quit || slices.Contains(tab.got, input.Start) {
		t.Fatalf("Menu, then Start: quit %v, the screen heard %v", a.quit, tab.got)
	}
}

// Alt+F4 is how Batocera's hotkeys, which Knulli has, ask a port to quit.
func TestAltF4Quits(t *testing.T) {
	a, _ := darkApp(nil)
	a.event(&sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_F4}})
	if a.quit {
		t.Fatal("F4 alone quit")
	}
	a.event(&sdl.KeyboardEvent{Type: sdl.KEYDOWN, Keysym: sdl.Keysym{Scancode: sdl.SCANCODE_F4, Mod: sdl.KMOD_LALT}})
	if !a.quit {
		t.Fatal("Alt+F4 didn't quit")
	}
}
