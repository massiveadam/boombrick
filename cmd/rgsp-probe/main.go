// Command rgsp-probe reports what Boombrick needs to know about the device
// it runs on: the screen, C library, sound cards, Bluetooth, input devices,
// battery, network tools, what Tailscale would need, the card's space, and
// what the equaliser costs on this CPU, and what a terminal with voice input
// would have to work with (microphones, USB host mode, kernel features). It only reads, and writes the report
// to standard output.
//
// It is built static, so it runs on BaseOS with nothing else. On the RG SP
// the Port "Boombrick Probe.sh" runs it and keeps the report in
// Roms/Ports/boombrick/logs/probe.txt.
package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ujjwal313/boombrick/internal/peq"
)

func main() {
	root := "/"
	if len(os.Args) > 1 {
		root = os.Args[1] // a copy of a device's files, for tests
	}
	report(os.Stdout, root, true)
}

// report writes every section; bench runs the EQ timing.
func report(w io.Writer, root string, bench bool) {
	p := func(r string) string { return filepath.Join(root, r) }
	fmt.Fprintf(w, "rgsp-probe %s, %s/%s, %d CPUs\n", time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())

	section(w, "System")
	line(w, "kernel", first(p("proc/version")))
	line(w, "os", field(p("etc/os-release"), "PRETTY_NAME="))
	line(w, "model", first(p("proc/device-tree/model")))
	line(w, "cpu", field(p("proc/cpuinfo"), "Hardware")+" "+strings.TrimSpace(countPrefix(p("proc/cpuinfo"), "processor"))+" cores")
	line(w, "cpu max kHz", first(p("sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq")))
	line(w, "governor", first(p("sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")))
	line(w, "memory", field(p("proc/meminfo"), "MemTotal:"))
	line(w, "glibc", glob(p("lib/libc-*.so"), p("lib/aarch64-linux-gnu/libc-*.so"), p("usr/lib/libc.so.6"), p("lib/libc.so.6")))

	section(w, "Screen")
	line(w, "fb0 virtual_size", first(p("sys/class/graphics/fb0/virtual_size")))
	line(w, "fb0 bits_per_pixel", first(p("sys/class/graphics/fb0/bits_per_pixel")))
	line(w, "/dev/disp", exists(p("dev/disp")))
	line(w, "Mali", glob(p("usr/lib/libmali*"), p("usr/lib/aarch64-linux-gnu/libmali*"), p("usr/lib/libEGL*")))

	section(w, "Sound")
	dump(w, p("proc/asound/cards"))
	dump(w, p("proc/asound/pcm"))
	line(w, "alsa-lib", glob(p("usr/lib/libasound.so*"), p("usr/lib/aarch64-linux-gnu/libasound.so*"), p("lib/libasound.so*")))
	line(w, "asound.conf", exists(p("etc/asound.conf")))
	line(w, "/dev/snd", list(p("dev/snd")))
	line(w, "wired (spk_state, 0 = plugged)", first(p("sys/class/power_supply/axp2202-battery/spk_state")))

	section(w, "Bluetooth")
	dump(w, p("tmp/tortos_btsink"))
	line(w, "bluealsa running", running(p("proc"), "bluealsa"))
	line(w, "bluetoothd running", running(p("proc"), "bluetoothd"))
	line(w, "dbus system socket", exists(p("run/dbus/system_bus_socket"), p("var/run/dbus/system_bus_socket")))

	section(w, "Input")
	devices(w, p("proc/bus/input/devices"))

	section(w, "Battery")
	for _, d := range globs(p("sys/class/power_supply/*")) {
		line(w, filepath.Base(d), first(filepath.Join(d, "type"))+" "+first(filepath.Join(d, "capacity"))+"% "+first(filepath.Join(d, "status")))
	}

	section(w, "Network and tools")
	for _, t := range []string{"wpa_cli", "iw", "ip", "udhcpc", "amixer", "aplay", "bluetoothctl", "bluealsa-aplay", "curl", "wget", "ssh", "dropbear", "rsync", "tailscale", "tailscaled", "busybox"} {
		path, err := exec.LookPath(t)
		if err != nil {
			path = "-"
		}
		line(w, t, path)
	}
	for _, d := range globs(p("sys/class/net/*")) {
		line(w, "net "+filepath.Base(d), first(filepath.Join(d, "operstate")))
	}

	section(w, "Tailscale needs")
	line(w, "/dev/net/tun", exists(p("dev/net/tun")))
	line(w, "tun module", glob(p("lib/modules/*/kernel/drivers/net/tun.ko*")))
	line(w, "iptables", which("iptables"))
	line(w, "note", "without a TUN device tailscaled runs in userspace mode: SSH into the device works, outbound goes through its proxy")

	section(w, "Storage")
	for _, m := range []string{"mnt/SDCARD", "mnt/sdcard", "mnt/mmc", "userdata", "tmp"} {
		var s syscall.Statfs_t
		if syscall.Statfs(p(m), &s) == nil {
			line(w, "/"+m, fmt.Sprintf("%.1f GB free of %.1f GB", float64(s.Bavail)*float64(s.Bsize)/1e9, float64(s.Blocks)*float64(s.Bsize)/1e9))
		}
	}
	mounts(w, p("proc/mounts"))

	cyberdeck(w, p)

	if bench {
		section(w, "Equaliser CPU (48 kHz stereo, 1024-frame chunks)")
		for _, n := range []int{10, 20} {
			line(w, fmt.Sprintf("%d filters", n), benchEQ(n))
		}
	}
}

func section(w io.Writer, name string) { fmt.Fprintf(w, "\n== %s\n", name) }
func line(w io.Writer, k, v string) {
	if v == "" {
		v = "-"
	}
	fmt.Fprintf(w, "%-34s %s\n", k+":", v)
}

func first(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := strings.TrimRight(strings.SplitN(string(b), "\n", 2)[0], "\x00 ")
	return strings.TrimSpace(s)
}

func field(path, prefix string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), prefix); ok {
			return strings.Trim(strings.TrimLeft(v, " \t:"), "\" ")
		}
	}
	return ""
}

func countPrefix(path, prefix string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return fmt.Sprint(n)
}

func dump(w io.Writer, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		line(w, path, "missing")
		return
	}
	fmt.Fprintf(w, "%s:\n", path)
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		fmt.Fprintf(w, "    %s\n", l)
	}
}

func globs(patterns ...string) []string {
	var out []string
	for _, p := range patterns {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

func glob(patterns ...string) string {
	var out []string
	for _, m := range globs(patterns...) {
		if t, err := os.Readlink(m); err == nil {
			m += " -> " + t
		}
		out = append(out, m)
	}
	return strings.Join(out, ", ")
}

func exists(paths ...string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return "yes (" + p + ")"
		}
	}
	return "no"
}

func list(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var n []string
	for _, e := range ents {
		n = append(n, e.Name())
	}
	return strings.Join(n, " ")
}

func which(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return "no"
}

// running reports whether a process named name runs, from /proc.
func running(proc, name string) string {
	for _, d := range globs(filepath.Join(proc, "[0-9]*")) {
		if first(filepath.Join(d, "comm")) == name {
			return "yes (pid " + filepath.Base(d) + ")"
		}
	}
	return "no"
}

// devices lists each input device's name, handlers and key capabilities.
func devices(w io.Writer, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		line(w, path, "missing")
		return
	}
	for _, block := range strings.Split(strings.TrimSpace(string(b)), "\n\n") {
		var name, handlers, keys string
		for _, l := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(l, "N: Name="):
				name = strings.Trim(strings.TrimPrefix(l, "N: Name="), "\"")
			case strings.HasPrefix(l, "H: Handlers="):
				handlers = strings.TrimPrefix(l, "H: Handlers=")
			case strings.HasPrefix(l, "B: KEY="):
				keys = strings.TrimPrefix(l, "B: KEY=")
			}
		}
		fmt.Fprintf(w, "    %-28s %-24s keys %s\n", name, strings.TrimSpace(handlers), keys)
	}
}

func mounts(w io.Writer, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && (strings.HasPrefix(f[1], "/mnt") || f[1] == "/" || strings.HasPrefix(f[1], "/userdata")) {
			fmt.Fprintf(w, "    %s on %s (%s)\n", f[0], f[1], f[2])
		}
	}
}

// benchEQ times n filters over 1024-frame chunks for about a second and
// gives the share of one core real-time playback needs.
func benchEQ(n int) string {
	prof := peq.Profile{Preamp: -6}
	for i := range n {
		f := 30 * math.Pow(600, float64(i)/float64(max(n-1, 1)))
		prof.Filters = append(prof.Filters, peq.Filter{On: true, Kind: peq.Peak, Freq: f, Gain: 3, Q: 1.4})
	}
	p := peq.NewProcessor()
	p.Set(prof, false)
	buf := make([]float32, 2*1024)
	for i := range buf {
		buf[i] = float32(0.3 * math.Sin(float64(i)*0.05))
	}
	start := time.Now()
	chunks := 0
	for time.Since(start) < time.Second {
		p.Process(buf, 48000)
		chunks++
	}
	per := time.Since(start) / time.Duration(chunks)
	share := float64(per) / float64(time.Second*1024/48000) * 100
	return fmt.Sprintf("%v per chunk, %.2f %% of one core", per.Round(time.Microsecond), share)
}

// cyberdeck reports what a terminal Port with push-to-talk would need: a
// microphone, USB host mode for a USB keyboard or mic, Bluetooth HID,
// memory for an agent CLI, and the kernel features those use.
func cyberdeck(w io.Writer, p func(string) string) {
	section(w, "Terminal and voice")
	var capture []string
	if b, err := os.ReadFile(p("proc/asound/pcm")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "capture") {
				capture = append(capture, strings.TrimSpace(l))
			}
		}
	}
	line(w, "capture PCMs", orNone(strings.Join(capture, "; ")))
	line(w, "capture devices", orNone(strings.Join(baseNames(globs(p("dev/snd/pcmC*D*c"))), " ")))
	line(w, "memory available", field(p("proc/meminfo"), "MemAvailable:"))
	line(w, "swap", field(p("proc/meminfo"), "SwapTotal:"))
	line(w, "zram", orNone(strings.Join(baseNames(globs(p("sys/block/zram*"))), " ")))

	// USB: host controllers show as root hubs (usb1, usb2...); a gadget
	// controller in /sys/class/udc means the port can act as a device.
	line(w, "usb root hubs", orNone(strings.Join(baseNames(globs(p("sys/bus/usb/devices/usb*"))), " ")))
	line(w, "usb devices", orNone(strings.Join(usbProducts(p), "; ")))
	line(w, "udc (gadget)", orNone(list(p("sys/class/udc"))))
	for _, f := range globs(p("sys/class/usb_role/*/role"), p("sys/devices/platform/*/dr_mode"),
		p("sys/devices/platform/*/*/dr_mode"), p("sys/devices/platform/soc*/usbc0/*role*"),
		p("sys/devices/platform/soc*/usbc0/usb_host"), p("sys/devices/platform/soc*/usbc0/otg_role")) {
		line(w, strings.TrimPrefix(f, p("")), first(f))
	}

	line(w, "bluetooth adapters", orNone(list(p("sys/class/bluetooth"))))
	line(w, "/dev/uinput", exists(p("dev/uinput"), p("dev/input/uinput")))
	line(w, "/dev/uhid", exists(p("dev/uhid")))
	line(w, "max user namespaces", orNone(first(p("proc/sys/user/max_user_namespaces"))))
	line(w, "LSMs", orNone(first(p("sys/kernel/security/lsm"))))
	var mods []string
	if b, err := os.ReadFile(p("proc/modules")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			name, _, _ := strings.Cut(l, " ")
			switch name {
			case "snd_usb_audio", "usbhid", "hidp", "uinput", "uhid", "hid_generic", "btusb", "zram":
				mods = append(mods, name)
			}
		}
	}
	line(w, "modules loaded", orNone(strings.Join(mods, " ")))
	line(w, "modules available", orNone(strings.Join(baseNames(globs(
		p("lib/modules/*/kernel/sound/usb/snd-usb-audio.ko*"), p("lib/modules/*/kernel/drivers/hid/usbhid/usbhid.ko*"),
		p("lib/modules/*/kernel/net/bluetooth/hidp/hidp.ko*"), p("lib/modules/*/kernel/drivers/input/misc/uinput.ko*"))), " ")))
	for _, kv := range kernelConfig(p("proc/config.gz"), "CONFIG_SND_USB_AUDIO", "CONFIG_USB_HID", "CONFIG_BT_HIDP",
		"CONFIG_INPUT_UINPUT", "CONFIG_UHID", "CONFIG_USB_MUSB_HDRC", "CONFIG_USB_OTG", "CONFIG_USER_NS",
		"CONFIG_SECURITY_LANDLOCK", "CONFIG_SECCOMP_FILTER", "CONFIG_SWAP", "CONFIG_ZRAM") {
		line(w, kv[0], kv[1])
	}
	for _, t := range []string{"arecord", "tmux", "mosh", "bash", "python3", "git", "sox", "ffmpeg", "node"} {
		line(w, t, which(t))
	}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func baseNames(paths []string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// usbProducts names the USB devices plugged in, root hubs left out.
func usbProducts(p func(string) string) []string {
	var out []string
	for _, d := range globs(p("sys/bus/usb/devices/*")) {
		if strings.HasPrefix(filepath.Base(d), "usb") || strings.Contains(filepath.Base(d), ":") {
			continue
		}
		name := first(filepath.Join(d, "product"))
		if name == "" {
			name = first(filepath.Join(d, "idVendor")) + ":" + first(filepath.Join(d, "idProduct"))
		}
		out = append(out, filepath.Base(d)+" "+name)
	}
	return out
}

// kernelConfig reads the named options from /proc/config.gz: "y", "m",
// "not set", or "unknown" without the file.
func kernelConfig(path string, names ...string) [][2]string {
	vals := map[string]string{}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		if z, err := gzip.NewReader(f); err == nil {
			sc := bufio.NewScanner(z)
			for sc.Scan() {
				l := sc.Text()
				if k, v, ok := strings.Cut(l, "="); ok && strings.HasPrefix(k, "CONFIG_") {
					vals[k] = v
				} else if strings.HasPrefix(l, "# CONFIG_") && strings.HasSuffix(l, " is not set") {
					vals[strings.TrimSuffix(strings.TrimPrefix(l, "# "), " is not set")] = "not set"
				}
			}
		}
	}
	var out [][2]string
	for _, n := range names {
		v, ok := vals[n]
		switch {
		case err != nil:
			v = "unknown (no /proc/config.gz)"
		case !ok:
			v = "absent"
		}
		out = append(out, [2]string{n, v})
	}
	return out
}
