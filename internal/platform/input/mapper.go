package input

import (
	"strconv"
	"strings"
)

// brickButtons is the Brick's joystick layout, from NextUI's tg5040
// platform.h.
var brickButtons = map[uint8]Button{
	0: B, 1: A, 2: Y, 3: X,
	4: L1, 5: R1,
	6: Select, 7: Start, 8: Menu,
	13: VolDown, 14: VolUp,
}

const (
	brickAxisL2      = 2
	brickAxisR2      = 5
	triggerThreshold = 16384
	scancodePower    = 102 // SDL_SCANCODE_POWER
)

// RGSPButtons is the Anbernic RG SP's built-in gamepad ("ANBERNIC-keys") as
// an SDL joystick under BaseOS, with plorpOS's SDL: SDL numbers its evdev
// buttons from BTN_JOYSTICK up, so b0..b8 are codes 304..312 and b9, b10 are
// 314, 315. Which code is which button was pressed and logged on an RG SP by
// plorpOS (src/platform_h700.c, 2026-10-06); X and Y cross their evdev names.
// The D-pad is hat 0. Not yet checked with Boombrick on the device.
var RGSPButtons = map[uint8]Button{
	0: A, 1: B, 2: Y, 3: X,
	4: L1, 5: R1,
	6: Select, 7: Start, 8: Menu,
	9: L2, 10: R2,
}

// controllerButtons maps SDL's game controller buttons, which are named by
// place, Xbox style: its A is the bottom face button, the Brick's B.
var controllerButtons = map[uint8]Button{
	0: B, 1: A, 2: Y, 3: X,
	4: Select, 5: Menu, 6: Start,
	9: L1, 10: R1,
	11: Up, 12: Down, 13: Left, 14: Right,
}

// labelledButtons are the face buttons when a mapping names them by the
// Brick's labels instead.
var labelledButtons = map[uint8]Button{0: A, 1: B, 2: X, 3: Y}

// SDL's game controller trigger axes.
const (
	controllerAxisL2 = 4
	controllerAxisR2 = 5
)

// SDL hat bits.
const (
	hatUp    = 1
	hatRight = 2
	hatDown  = 4
	hatLeft  = 8
)

// desktopKeys maps SDL scancodes for development on a PC; README.md lists them.
var desktopKeys = map[int]Button{
	82: Up, 81: Down, 80: Left, 79: Right,
	14: A, 13: B, 12: X, 24: Y, // K J I U
	20: L1, 8: R1, // Q E
	30: L2, 32: R2, // 1 3
	40: Start, 43: Select, 41: Menu, // Return Tab Escape
	19: Power, 45: VolDown, 46: VolUp, // P - =
	scancodePower: Power,
}

// Mapper turns device events into button events. It remembers hat and trigger
// state so each physical change produces exactly one Press or Release.
type Mapper struct {
	// Labelled says a game controller mapping names the face buttons by the
	// Brick's labels, as Knulli's does for ports with its Xbox layout
	// switched on, rather than by place.
	Labelled bool
	// Buttons numbers the joystick's buttons; nil for the Brick's.
	Buttons map[uint8]Button
	hat     uint8
	l2, r2  bool
}

// JoyButton maps a joystick button.
func (m *Mapper) JoyButton(button uint8, down bool) []Event {
	buttons := brickButtons
	if m.Buttons != nil {
		buttons = m.Buttons
	}
	b, ok := buttons[button]
	if !ok {
		return nil
	}
	return []Event{{Button: b, Kind: kind(down)}}
}

// JoyHat maps the D-pad hat's new value.
func (m *Mapper) JoyHat(value uint8) []Event {
	var out []Event
	for _, h := range [...]struct {
		bit uint8
		b   Button
	}{{hatUp, Up}, {hatRight, Right}, {hatDown, Down}, {hatLeft, Left}} {
		was, is := m.hat&h.bit != 0, value&h.bit != 0
		if was != is {
			out = append(out, Event{Button: h.b, Kind: kind(is)})
		}
	}
	m.hat = value
	return out
}

// JoyAxis maps the analogue L2 and R2 triggers to button presses.
func (m *Mapper) JoyAxis(axis uint8, value int16) []Event {
	switch axis {
	case brickAxisL2:
		return trigger(&m.l2, L2, value)
	case brickAxisR2:
		return trigger(&m.r2, R2, value)
	}
	return nil
}

// ControllerButton maps a game controller button, numbered as SDL's are.
func (m *Mapper) ControllerButton(button uint8, down bool) []Event {
	b, ok := controllerButtons[button]
	if !ok {
		return nil
	}
	if l, face := labelledButtons[button]; face && m.Labelled {
		b = l
	}
	return []Event{{Button: b, Kind: kind(down)}}
}

// ControllerAxis maps a game controller's triggers to L2 and R2; its sticks
// go unused.
func (m *Mapper) ControllerAxis(axis uint8, value int16) []Event {
	switch axis {
	case controllerAxisL2:
		return trigger(&m.l2, L2, value)
	case controllerAxisR2:
		return trigger(&m.r2, R2, value)
	}
	return nil
}

// MappedButtons is the set of joystick buttons an SDL game controller
// mapping, "GUID,name,a:b0,...", reads, by their numbers.
func MappedButtons(mapping string) map[uint8]bool {
	buttons := make(map[uint8]bool)
	fields := strings.Split(mapping, ",")
	for _, f := range fields[min(2, len(fields)):] { // past the GUID and name
		_, in, _ := strings.Cut(f, ":")
		n, ok := strings.CutPrefix(in, "b")
		if !ok {
			continue
		}
		if b, err := strconv.ParseUint(n, 10, 8); err == nil {
			buttons[uint8(b)] = true
		}
	}
	return buttons
}

// trigger turns a trigger's new value into a press or release of b, once
// it crosses halfway; state is whether it was down.
func trigger(state *bool, b Button, value int16) []Event {
	down := int(value) > triggerThreshold
	if down == *state {
		return nil
	}
	*state = down
	return []Event{{Button: b, Kind: kind(down)}}
}

// Key maps a keyboard scancode. OS key repeat is ignored because Repeater
// produces repeats for every input device.
func (m *Mapper) Key(scancode int, down, osRepeat bool) []Event {
	if osRepeat {
		return nil
	}
	b, ok := desktopKeys[scancode]
	if !ok {
		return nil
	}
	return []Event{{Button: b, Kind: kind(down)}}
}

func kind(down bool) Kind {
	if down {
		return Press
	}
	return Release
}
