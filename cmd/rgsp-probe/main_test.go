package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The report reads a device's files from a root and says what it found,
// and what is missing, without failing.
func TestReport(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"proc/version":                                    "Linux version 4.9.170 (rgsp)\n",
		"sys/class/graphics/fb0/virtual_size":             "720,960\n",
		"proc/asound/cards":                               " 0 [audiocodec     ]: audiocodec - audiocodec\n",
		"tmp/tortos_btsink":                               "bluealsa:DEV=AA,PROFILE=a2dp\n",
		"sys/class/power_supply/axp2202-battery/type":     "Battery\n",
		"sys/class/power_supply/axp2202-battery/capacity": "81\n",
		"sys/class/power_supply/axp2202-battery/status":   "Discharging\n",
		"proc/bus/input/devices":                          "N: Name=\"ANBERNIC-keys\"\nH: Handlers=event1 js0\nB: KEY=1ff\n\nN: Name=\"axp2202-pek\"\nH: Handlers=event0\nB: KEY=100000 0 0 0\n",
	}
	for p, c := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	var b strings.Builder
	report(&b, root, false)
	out := b.String()
	for _, want := range []string{"Linux version 4.9.170", "720,960", "audiocodec", "bluealsa:DEV=AA",
		"axp2202-battery:", "81% Discharging", "ANBERNIC-keys", "event1 js0", "/dev/net/tun:", "no"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.Contains(out, "panic") {
		t.Fatal(out)
	}
}
