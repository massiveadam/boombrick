package musicsync

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Gate decides whether an automatic sync may run now: only on a home
// network, and only with the battery charging or well charged, so syncing
// costs the battery little. Its functions are replaceable for tests.
type Gate struct {
	HomeSSIDs  []string
	MinBattery int // percent; ignored while charging
	SSID       func() (string, bool)
	Battery    func() (percent int, charging, ok bool)
}

// Allow reports whether to sync, and why not.
func (g Gate) Allow() (bool, string) {
	ssid, ok := g.SSID()
	if !ok {
		return false, "not on Wi-Fi"
	}
	if !slices.Contains(g.HomeSSIDs, ssid) {
		return false, "not on a home network"
	}
	if pct, charging, ok := g.Battery(); ok && !charging && pct < g.MinBattery {
		return false, "battery low and not charging"
	}
	return true, ""
}

// CurrentSSID is the Wi-Fi network wlan0 is joined to, asked of wpa_cli or
// iw, whichever the firmware has. $RGSP_SSID stands in for both on a desktop.
// BaseOS has wpa_cli and no iw (the probe, 2026-11-08); plorpOS starts
// wpa_supplicant with its control socket in /run/wpa_supplicant, which
// wpa_cli is told, as it looks elsewhere by default.
func CurrentSSID() (string, bool) {
	if s, ok := os.LookupEnv("RGSP_SSID"); ok {
		return s, s != ""
	}
	for _, args := range [][]string{
		{"-p", "/run/wpa_supplicant", "-i", "wlan0", "status"},
		{"-i", "wlan0", "status"},
	} {
		out, err := exec.Command("wpa_cli", args...).Output()
		if err != nil {
			continue
		}
		if s := field(out, "ssid="); s != "" && strings.Contains(string(out), "wpa_state=COMPLETED") {
			return s, true
		}
	}
	if out, err := exec.Command("iw", "dev", "wlan0", "link").Output(); err == nil {
		if s := field(out, "SSID: "); s != "" {
			return s, true
		}
	}
	return "", false
}

func field(out []byte, prefix string) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), prefix); ok {
			return v
		}
	}
	return ""
}

// BatteryFrom reads the battery under sysfs (/sys normally): the first
// power supply of type Battery.
func BatteryFrom(sysfs string) func() (int, bool, bool) {
	return func() (int, bool, bool) {
		dirs, _ := filepath.Glob(filepath.Join(sysfs, "class", "power_supply", "*"))
		for _, d := range dirs {
			if t, _ := os.ReadFile(filepath.Join(d, "type")); strings.TrimSpace(string(t)) != "Battery" {
				continue
			}
			c, err := os.ReadFile(filepath.Join(d, "capacity"))
			if err != nil {
				continue
			}
			pct, err := strconv.Atoi(strings.TrimSpace(string(c)))
			if err != nil {
				continue
			}
			s, _ := os.ReadFile(filepath.Join(d, "status"))
			st := strings.TrimSpace(string(s))
			return pct, st == "Charging" || st == "Full", true
		}
		return 0, false, false
	}
}
