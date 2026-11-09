package platform

import (
	"os"
	"path/filepath"
	"syscall"
)

// Backlight turns the panel's light off and back on, as NextUI does on the
// Brick: by setting the display driver's brightness to 0, having read it
// first so that it can be put back.
type Backlight struct {
	dev   display
	saved int
	off   bool
}

// display is the display driver's brightness control.
type display interface {
	brightness() (int, error)
	setBrightness(v int) error
}

// fallbackBrightness is put back when the brightness read before turning the
// light off was already 0: NextUI's level 6 of 10 on the Brick.
const fallbackBrightness = 96

// wakeBrightness is set before the level is put back: the Brick's panel stays
// dark when set straight from 0, so NextUI wakes it by way of this level.
const wakeBrightness = 8

// NewBacklight returns the Brick's backlight. On a desktop there is no
// /dev/disp, and Off fails.
func NewBacklight() *Backlight { return &Backlight{dev: dispDevice{path: "/dev/disp"}} }

// Off turns the light off, remembering how bright it was.
func (b *Backlight) Off() error {
	if b.off {
		return nil
	}
	v, err := b.dev.brightness()
	if err != nil {
		return err
	}
	if err := b.dev.setBrightness(0); err != nil {
		return err
	}
	if v <= 0 {
		v = fallbackBrightness
	}
	b.saved, b.off = v, true
	return nil
}

// dimBrightness is the dimmest the driver is asked for: 1, below the bottom
// rung of plorpOS's ladder (2, src/platform_h700.c bright_ladder). 0 is off.
const dimBrightness = 1

// Dim turns the light down to level, still lit (at least dimBrightness),
// remembering how bright it was; On puts it back, as after Off. A light
// already as dim is left alone.
func (b *Backlight) Dim(level int) error {
	level = max(level, dimBrightness)
	if b.off {
		return nil
	}
	v, err := b.dev.brightness()
	if err != nil {
		return err
	}
	if v <= level {
		return nil // as dim already: nothing to put back
	}
	if err := b.dev.setBrightness(level); err != nil {
		return err
	}
	b.saved, b.off = v, true
	return nil
}

// On puts the light back as it was before Off.
func (b *Backlight) On() error {
	if !b.off {
		return nil
	}
	if err := b.dev.setBrightness(wakeBrightness); err != nil {
		return err
	}
	if err := b.dev.setBrightness(b.saved); err != nil {
		return err
	}
	b.off = false
	return nil
}

// PowerOff asks the firmware to shut the device down once Boombrick quits.
// On NextUI it does as NextUI's own apps do when Power is held: without
// nextui_exec its launcher stops starting the menu again, and with poweroff
// it shuts down. On CrossMix it leaves boombrick-poweroff for Boombrick's
// launch.sh, which powers off when Boombrick has exited. Elsewhere it does
// nothing: Knulli keeps Power for itself.
func (p Profile) PowerOff() error {
	switch p.Firmware {
	case NextUI:
		if err := os.Remove(filepath.Join(p.tmp, "nextui_exec")); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := touch(filepath.Join(p.tmp, "poweroff")); err != nil {
			return err
		}
		syscall.Sync()
	case CrossMix:
		return touch(filepath.Join(p.tmp, "boombrick-poweroff"))
	}
	return nil
}

// touch creates an empty file at path.
func touch(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return f.Close()
}
