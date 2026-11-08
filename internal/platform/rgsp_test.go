package platform

import (
	"testing"

	"github.com/ujjwal313/boombrick/internal/platform/input"
)

func TestDetectRGSP(t *testing.T) {
	t.Setenv("BOOMBRICK_PLATFORM", "rgsp")
	t.Setenv("SDCARD_PATH", "")
	p := Detect("/mnt/SDCARD/Roms/Ports/boombrick")
	if p.Firmware != RGSP || !p.Paths.OnDevice {
		t.Fatalf("%+v", p)
	}
	if p.Paths.Music != "/mnt/SDCARD/Music" || p.Paths.Data != "/mnt/SDCARD/Roms/Ports/boombrick/data" {
		t.Fatalf("paths %+v", p.Paths)
	}
	if l := p.Pads(); l.Joystick.Buttons()[0] != input.A || l.Controllers {
		t.Fatalf("pads %+v", l)
	}
	if !p.FirmwarePower() || p.Device() != "Anbernic RG SP" || p.ReleaseAsset != "" {
		t.Fatal("power, name or updates wrong")
	}
}
