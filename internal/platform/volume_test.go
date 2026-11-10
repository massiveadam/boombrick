package platform

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// settingsFile writes a SettingsV11 prefix with the fields set, by index.
func settingsFile(t *testing.T, path string, n int, fields map[int]int32) {
	t.Helper()
	b := make([]byte, n*4)
	for i, v := range fields {
		binary.LittleEndian.PutUint32(b[i*4:], uint32(v))
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// readOnce reads the volume from path through a file of its own.
func readOnce(path string) (int, bool) {
	f := &volumeFile{path: path}
	defer f.close()
	return f.read()
}

func TestNextUIVolume(t *testing.T) {
	base := func(over map[int]int32) map[int]int32 {
		m := map[int]int32{settingsVersion: 11, settingsHeadphone: 7, settingsSpeaker: 12, settingsToggled: muteNoChange}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name   string
		n      int
		fields map[int]int32
		want   int
		ok     bool
	}{
		{"speaker", 40, base(nil), 12, true},
		{"jack means headphones", 40, base(map[int]int32{settingsJack: 1}), 7, true},
		{"bluetooth sink means headphones", 40, base(map[int]int32{settingsSink: 1}), 7, true},
		{"muted with a volume to return to", 40, base(map[int]int32{settingsMute: 1, settingsToggled: 0}), 0, true},
		{"muted with nothing to return to", 40, base(map[int]int32{settingsMute: 1}), 12, true},
		{"just the fields read", 29, base(nil), 12, true},
		{"wrong version", 40, base(map[int]int32{settingsVersion: 10}), 0, false},
		{"short file", 28, base(nil), 0, false},
		{"above 20", 40, base(map[int]int32{settingsSpeaker: 21}), 0, false},
		{"below 0", 40, base(map[int]int32{settingsSpeaker: -1}), 0, false},
	}
	for _, c := range cases {
		p := filepath.Join(t.TempDir(), "SharedSettings")
		settingsFile(t, p, c.n, c.fields)
		if got, ok := readOnce(p); got != c.want || ok != c.ok {
			t.Errorf("%s: %d, %v; want %d, %v", c.name, got, ok, c.want, c.ok)
		}
	}
	if _, ok := readOnce(filepath.Join(t.TempDir(), "missing")); ok {
		t.Error("a missing file read ok")
	}
}

// The settings file stays open between readings, which see it change; one
// that fails lets it go, and the next opens it again.
func TestVolumeFileStaysOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "SharedSettings")
	set := func(v int32) {
		settingsFile(t, p, 29, map[int]int32{settingsVersion: 11, settingsSpeaker: v, settingsToggled: muteNoChange})
	}
	set(5)
	v := &volumeFile{path: p}
	defer v.close()
	if l, ok := v.read(); l != 5 || !ok {
		t.Fatalf("first reading %d, %v", l, ok)
	}
	f := v.f
	set(8)
	if l, ok := v.read(); l != 8 || !ok || v.f != f {
		t.Fatalf("after a change: %d, %v, the same file %v", l, ok, v.f == f)
	}
	settingsFile(t, p, 10, nil)
	if _, ok := v.read(); ok || v.f != nil {
		t.Fatalf("a short file read %v, file kept %v", ok, v.f != nil)
	}
	set(9)
	if l, ok := v.read(); l != 9 || !ok {
		t.Fatalf("after reopening: %d, %v", l, ok)
	}
}

func TestVolumeSettingsIsNextUIs(t *testing.T) {
	if got := (Profile{Firmware: NextUI}).VolumeSettings(); got != "/dev/shm/SharedSettings" {
		t.Errorf("NextUI: %q", got)
	}
	for _, f := range []Firmware{Desktop, CrossMix} {
		if got := (Profile{Firmware: f}).VolumeSettings(); got != "" {
			t.Errorf("%v: %q", f, got)
		}
	}
}

// fakeVolume is a volume the test sets, which counts its readings.
type fakeVolume struct {
	mu     sync.Mutex
	level  int
	ok     bool
	reads  int
	closed bool
}

func (f *fakeVolume) read() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.level, f.ok
}

func (f *fakeVolume) close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

func (f *fakeVolume) set(level int, ok bool) { f.mu.Lock(); f.level, f.ok = level, ok; f.mu.Unlock() }

func (f *fakeVolume) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.reads }

// Left alone the volume is read every 2 s; after a press, at once and every
// 150 ms for a moment; while paused, never. The first reading is the starting
// point, as is the first after a pause; readings that fail are skipped.
func TestVolumeWatcher(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &fakeVolume{level: 5, ok: true}
		w, ok := newVolumeWatcher(src)
		if !ok || src.count() != 1 {
			t.Fatalf("starting point read %v, %d times", ok, src.count())
		}
		var mu sync.Mutex
		var got []int
		heard := func() []int { mu.Lock(); defer mu.Unlock(); return slices.Clone(got) }
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			w.Watch(ctx, func(l int) { mu.Lock(); got = append(got, l); mu.Unlock() })
		}()
		// reads does do, then waits d, and says how many readings were made.
		reads := func(do func(), d time.Duration) int {
			n := src.count()
			do()
			time.Sleep(d)
			synctest.Wait()
			return src.count() - n
		}
		nothing := func() {}

		if n := reads(nothing, 10*time.Second+time.Millisecond); n != 5 || len(heard()) != 0 {
			t.Fatalf("left alone for 10 s: %d readings, heard %v", n, heard())
		}
		src.set(8, true)
		if n := reads(w.Pressed, 0); n != 1 || !slices.Equal(heard(), []int{8}) {
			t.Fatalf("pressed: %d readings, heard %v", n, heard())
		}
		if n := reads(nothing, time.Second); n != 6 {
			t.Fatalf("%d readings in the second after a press, want 6", n)
		}
		if n := reads(nothing, time.Second); n != 4 {
			t.Fatalf("%d readings in the next second, want 4, the last at 1.5 s", n)
		}
		src.set(9, false)
		if n := reads(nothing, 2*time.Second); n != 1 || len(heard()) != 1 {
			t.Fatalf("slow again: %d readings, heard %v", n, heard())
		}

		src.set(12, true)
		if n := reads(func() { w.Pause(true) }, time.Minute); n != 0 {
			t.Fatalf("%d readings while paused", n)
		}
		if n := reads(func() { w.Pause(false) }, 0); n != 1 || len(heard()) != 1 {
			t.Fatalf("let go: %d readings, heard %v; want one, as a new starting point", n, heard())
		}
		src.set(13, true)
		if reads(nothing, volumeFast+time.Millisecond); !slices.Equal(heard(), []int{8, 13}) {
			t.Fatalf("after the pause, heard %v", heard())
		}

		cancel()
		<-done
		if !src.closed {
			t.Fatal("the file was left open")
		}
	})
}

func TestPlorpOSVolume(t *testing.T) {
	dir := t.TempDir()
	p := Profile{Firmware: RGSP, tmp: dir}
	level := p.VolumeSettings()
	if err := os.WriteFile(level, []byte("7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, ok := NewVolumeWatcher(level)
	if !ok || w.last != 7 {
		t.Fatalf("read %d, %v; want 7", w.last, ok)
	}
	// Replaced whole, as plorpOS writes it: the next reading sees the new one.
	os.WriteFile(level+".tmp", []byte("12\n"), 0o644)
	os.Rename(level+".tmp", level)
	if v, ok := w.src.read(); !ok || v != 12 {
		t.Errorf("after the change: %d, %v; want 12", v, ok)
	}
	os.WriteFile(level, []byte("21\n"), 0o644)
	if _, ok := w.src.read(); ok {
		t.Error("21 read as a level")
	}

	ctl := p.VolumeControl()
	if StepVolume(ctl, true) {
		t.Error("stepped with no FIFO")
	}
	if err := syscall.Mkfifo(ctl, 0o600); err != nil {
		t.Fatal(err)
	}
	if StepVolume(ctl, true) {
		t.Error("stepped with nobody reading")
	}
	r, err := os.OpenFile(ctl, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !StepVolume(ctl, true) || !StepVolume(ctl, false) {
		t.Fatal("not stepped")
	}
	b := make([]byte, 8)
	if n, _ := r.Read(b); string(b[:n]) != "+-" {
		t.Errorf("read %q, want \"+-\"", b[:n])
	}
}
