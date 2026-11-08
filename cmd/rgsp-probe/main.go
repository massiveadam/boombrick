// Command rgsp-probe reports what Boombrick needs to know about the device
// it runs on: the screen, C library, sound cards, Bluetooth, input devices,
// battery, network tools, what Tailscale would need, the card's space, and
// what the equaliser costs on this CPU. It only reads, and writes the report
// to standard output.
//
// It is built static, so it runs on BaseOS with nothing else. On the RG SP
// the Port "Boombrick Probe.sh" runs it and keeps the report in
// Roms/Ports/boombrick/logs/probe.txt.
package main

import (
	"bufio"
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
