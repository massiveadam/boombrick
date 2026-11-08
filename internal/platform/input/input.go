// Package input maps raw SDL input from the Brick, or a desktop keyboard, to
// logical buttons and generates D-pad auto-repeat.
package input

import "strconv"

// Button is a logical button on the Brick.
type Button uint8

const (
	None Button = iota
	Up
	Down
	Left
	Right
	A
	B
	X
	Y
	L1
	R1
	L2
	R2
	Start
	Select
	Menu
	Power
	VolUp
	VolDown
)

var buttonNames = [...]string{"None", "Up", "Down", "Left", "Right", "A", "B", "X", "Y",
	"L1", "R1", "L2", "R2", "Start", "Select", "Menu", "Power", "VolUp", "VolDown"}

// String returns the button name, for logs.
func (b Button) String() string {
	if int(b) < len(buttonNames) {
		return buttonNames[b]
	}
	return "Button(" + strconv.Itoa(int(b)) + ")"
}

// IsDpad reports whether b is a D-pad direction.
func (b Button) IsDpad() bool { return b >= Up && b <= Right }

// Kind is what happened to a button.
type Kind uint8

const (
	Press Kind = iota
	Release
	Repeat
)

// Event is one button change.
type Event struct {
	Button Button
	Kind   Kind
}

// Layout is how a firmware hands over the Brick's buttons.
type Layout struct {
	// Controllers reads the pads through the game controller mappings SDL
	// is given, as Knulli gives one to each port, instead of by NextUI's
	// button numbers.
	Controllers bool
	// Labelled is Mapper.Labelled.
	Labelled bool
	// Joystick is how the joystick's buttons are numbered.
	Joystick Joystick
}

// Joystick names a numbering of a built-in gamepad's buttons.
type Joystick uint8

const (
	BrickJoystick Joystick = iota // NextUI's tg5040 numbering
	RGSPJoystick                  // see RGSPButtons
)

// Buttons is the numbering's map, for Mapper.Buttons; nil for the Brick's.
func (j Joystick) Buttons() map[uint8]Button {
	if j == RGSPJoystick {
		return RGSPButtons
	}
	return nil
}
