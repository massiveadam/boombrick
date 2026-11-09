package platform

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// panel stands in for the display driver, keeping the levels it was set to.
type panel struct {
	level int
	fail  bool
	set   []int
}

func (p *panel) brightness() (int, error) { return p.level, nil }

func (p *panel) setBrightness(v int) error {
	if p.fail {
		return errors.New("driver refused")
	}
	p.level = v
	p.set = append(p.set, v)
	return nil
}

// The Brick's panel comes back from 0 by way of a low level, as NextUI wakes
// it, then takes the level it had.
func TestBacklightOnGoesByWayOfALowLevel(t *testing.T) {
	p := &panel{level: 128}
	b := &Backlight{dev: p}
	b.Off()
	p.set = nil
	if err := b.On(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.set, []int{wakeBrightness, 128}) {
		t.Fatalf("On set the levels %v, want %d then 128", p.set, wakeBrightness)
	}
}

func TestBacklightOffAndOn(t *testing.T) {
	p := &panel{level: 128}
	b := &Backlight{dev: p}
	if err := b.Off(); err != nil || p.level != 0 {
		t.Fatalf("Off: level %d, err %v; want 0", p.level, err)
	}
	if err := b.Off(); err != nil || p.level != 0 {
		t.Fatalf("Off again: level %d, err %v", p.level, err)
	}
	if err := b.On(); err != nil || p.level != 128 {
		t.Fatalf("On: level %d, err %v; want 128 back", p.level, err)
	}
	if err := b.On(); err != nil || p.level != 128 {
		t.Fatalf("On again: level %d, err %v", p.level, err)
	}
}

func TestBacklightOnAfterADarkStart(t *testing.T) {
	p := &panel{}
	b := &Backlight{dev: p}
	b.Off()
	if b.On(); p.level != fallbackBrightness {
		t.Fatalf("level %d after On, want %d", p.level, fallbackBrightness)
	}
}

func TestBacklightOffFailing(t *testing.T) {
	p := &panel{level: 64, fail: true}
	b := &Backlight{dev: p}
	if err := b.Off(); err == nil {
		t.Fatal("Off reported no error")
	}
	p.fail = false
	if b.On(); p.level != 64 {
		t.Fatalf("On after a failed Off set the level to %d", p.level)
	}
}

func TestNoDisplayDriver(t *testing.T) {
	b := &Backlight{dev: dispDevice{path: filepath.Join(t.TempDir(), "disp")}}
	if err := b.Off(); err == nil {
		t.Fatal("Off without a display driver reported no error")
	}
}

func TestPowerOffOnNextUI(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "nextui_exec"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p := Profile{Firmware: NextUI, tmp: tmp}
	if err := p.PowerOff(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "nextui_exec")); !os.IsNotExist(err) {
		t.Error("nextui_exec is still there")
	}
	if _, err := os.Stat(filepath.Join(tmp, "poweroff")); err != nil {
		t.Errorf("poweroff wasn't written: %v", err)
	}
	if err := p.PowerOff(); err != nil {
		t.Errorf("a second PowerOff failed: %v", err)
	}
}

// On CrossMix, Boombrick's own launch.sh powers off once Boombrick has quit,
// when it finds the flag.
func TestPowerOffOnCrossMix(t *testing.T) {
	tmp := t.TempDir()
	if err := (Profile{Firmware: CrossMix, tmp: tmp}).PowerOff(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "boombrick-poweroff")); err != nil {
		t.Errorf("the flag wasn't written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "poweroff")); !os.IsNotExist(err) {
		t.Error("NextUI's poweroff file was written")
	}
}

func TestPowerOffOnADesktop(t *testing.T) {
	tmp := t.TempDir()
	if err := (Profile{Firmware: Desktop, tmp: tmp}).PowerOff(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("wrote %d files", len(entries))
	}
}

// Dim lowers the light to its dimmest, still lit, and On brings back the level
// it had; screen off from dim, then on, still comes back to that level.
func TestBacklightDim(t *testing.T) {
	p := &panel{level: 160}
	b := &Backlight{dev: p}
	if err := b.Dim(0); err != nil || p.level != dimBrightness {
		t.Fatalf("dim: %v, level %d", err, p.level)
	}
	if err := b.Off(); err != nil || p.level != dimBrightness {
		t.Fatalf("off from dim changed it: %v %d", err, p.level)
	}
	if err := b.On(); err != nil || p.level != 160 {
		t.Fatalf("on: %v, level %d", err, p.level)
	}
	// Already as dim as it goes: nothing to put back.
	p.level = 8
	if err := b.Dim(16); err != nil || b.off {
		t.Fatalf("dim at the bottom: %v off=%v", err, b.off)
	}
}

func TestBacklightDimToALevel(t *testing.T) {
	p := &panel{level: 160}
	b := &Backlight{dev: p}
	if err := b.Dim(16); err != nil || p.level != 16 {
		t.Fatalf("dim to 16: %v, level %d", err, p.level)
	}
	if err := b.On(); err != nil || p.level != 160 {
		t.Fatalf("on: %v %d", err, p.level)
	}
}
