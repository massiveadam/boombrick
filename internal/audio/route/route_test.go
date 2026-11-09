package route

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/audio"
)

func TestResolveFollowsPlorpOS(t *testing.T) {
	cases := []struct {
		st   State
		want Sink
	}{
		{State{}, Sink{Speaker, "default"}},
		{State{Wired: true}, Sink{Wired, "default"}},
		{State{Bluetooth: "bluealsa:DEV=AA,PROFILE=a2dp"}, Sink{Bluetooth, "bluealsa:DEV=AA,PROFILE=a2dp"}},
		// A cable beats a headset that is merely connected.
		{State{Wired: true, Bluetooth: "bt"}, Sink{Wired, "default"}},
		// A DAC beats everything.
		{State{USB: "plughw:CARD=KA13,DEV=0", Wired: true, Bluetooth: "bt"}, Sink{USB, "plughw:CARD=KA13,DEV=0"}},
	}
	for _, c := range cases {
		if got := Resolve(c.st); got != c.want {
			t.Errorf("%+v: %+v, want %+v", c.st, got, c.want)
		}
	}
}

// dev is a fake device.
type dev struct {
	name          string
	opens, writes int
	closed        bool
	busyFor       int // opens to refuse as busy
}

type busyErr struct{}

func (busyErr) Error() string { return "busy" }
func (busyErr) Busy() bool    { return true }

func (d *dev) Open(int) error {
	d.opens++
	if d.busyFor > 0 {
		d.busyFor--
		return busyErr{}
	}
	d.closed = false
	return nil
}
func (d *dev) Write([]int16) error { d.writes++; return nil }
func (d *dev) Drop() (int, error)  { return 0, nil }
func (d *dev) Close() error        { d.closed = true; return nil }

type rig struct {
	mu    sync.Mutex
	st    State
	devs  map[string]*dev
	clock time.Time
	r     *Router
}

func newRig(st State) *rig {
	g := &rig{st: st, devs: map[string]*dev{}, clock: time.Unix(0, 0)}
	g.r = New(func() State { g.mu.Lock(); defer g.mu.Unlock(); return g.st },
		func(name string) audio.Output {
			d := g.devs[name]
			if d == nil {
				d = &dev{name: name}
				g.devs[name] = d
			}
			return d
		}, nil)
	g.r.now = func() time.Time { return g.clock }
	g.r.sleep = func(d time.Duration) { g.clock = g.clock.Add(d) }
	return g
}

func (g *rig) set(st State) { g.mu.Lock(); g.st = st; g.mu.Unlock() }
func (g *rig) tick()        { g.clock = g.clock.Add(time.Second) }

func TestHeadsetLossPauses(t *testing.T) {
	g := newRig(State{Bluetooth: "bt"})
	if err := g.r.Open(48000); err != nil {
		t.Fatal(err)
	}
	g.r.Write(make([]int16, 4))
	g.set(State{}) // the AirPods went back in their case
	g.tick()
	if err := g.r.Write(make([]int16, 4)); !errors.Is(err, ErrSinkLost) {
		t.Fatalf("write after loss: %v", err)
	}
	if !g.devs["bt"].closed || g.devs["default"] != nil {
		t.Fatal("kept playing, or moved to the speaker by itself")
	}
	// Resume opens wherever is next: the speaker.
	if err := g.r.Open(48000); err != nil || g.r.Sink().Kind != Speaker {
		t.Fatalf("resume: %v %+v", err, g.r.Sink())
	}
}

func TestBetterSinkTakesOver(t *testing.T) {
	g := newRig(State{})
	g.r.Open(48000)
	g.set(State{Bluetooth: "bt"})
	g.r.Write(make([]int16, 4)) // not checked yet
	if g.devs["bt"] != nil {
		t.Fatal("checked state on every write")
	}
	g.tick()
	if err := g.r.Write(make([]int16, 4)); err != nil {
		t.Fatal(err)
	}
	if g.r.Sink().Kind != Bluetooth || !g.devs["default"].closed || g.devs["bt"].writes != 1 {
		t.Fatalf("sink %+v", g.r.Sink())
	}
	// A cable then wins over the headset, without pausing.
	g.set(State{Bluetooth: "bt", Wired: true})
	g.tick()
	if err := g.r.Write(make([]int16, 4)); err != nil || g.r.Sink().Kind != Wired {
		t.Fatalf("%v %+v", err, g.r.Sink())
	}
}

func TestWaitsForBusyCodec(t *testing.T) {
	g := newRig(State{})
	g.devs["default"] = &dev{name: "default", busyFor: 5} // a game lets go after half a second
	if err := g.r.Open(48000); err != nil {
		t.Fatalf("open: %v", err)
	}
	if d := g.devs["default"]; d.opens != 6 {
		t.Fatalf("%d opens", d.opens)
	}
	// One that never lets go fails after about a second.
	h := newRig(State{})
	h.devs["default"] = &dev{name: "default", busyFor: 1000}
	start := h.clock
	if err := h.r.Open(48000); err == nil || !isBusy(err) {
		t.Fatalf("open: %v", err)
	}
	if waited := h.clock.Sub(start); waited < time.Second || waited > 1200*time.Millisecond {
		t.Fatalf("waited %v", waited)
	}
}

// The engine opens before every write (Engine.write). A repeated Open keeps
// the sink playing, so Write still sees the headset go and pauses.
func TestEngineCallPatternStillPausesOnLoss(t *testing.T) {
	g := newRig(State{Bluetooth: "bt"})
	step := func() error {
		if err := g.r.Open(48000); err != nil {
			return err
		}
		return g.r.Write(make([]int16, 4))
	}
	if err := step(); err != nil {
		t.Fatal(err)
	}
	g.set(State{})
	g.tick()
	if err := step(); !errors.Is(err, ErrSinkLost) {
		t.Fatalf("after the headset went: %v, sink %+v", err, g.r.Sink())
	}
	if g.devs["default"] != nil {
		t.Fatal("carried on out of the speaker")
	}
}

func TestResolveChoice(t *testing.T) {
	all := State{USB: "plughw:CARD=KA13,DEV=0", Wired: true, Bluetooth: "bt_AA"}
	cases := []struct {
		name string
		st   State
		want Sink
	}{
		{"auto keeps the rules", all, Sink{USB, "plughw:CARD=KA13,DEV=0"}},
		{"this device with a plug", State{Wired: true, Bluetooth: "bt_AA", Choice: ThisOne}, Sink{Wired, "default"}},
		{"this device over a headset", State{Bluetooth: "bt_AA", Choice: ThisOne}, Sink{Speaker, "default"}},
		{"the headset over a DAC and a plug", State{USB: "u", Wired: true, Bluetooth: "bt_AA", Choice: ChooseBT}, Sink{Bluetooth, "bt_AA"}},
		{"a headset that is gone falls back to auto", State{Wired: true, Choice: ChooseBT}, Sink{Wired, "default"}},
		{"a DAC that is gone falls back to auto", State{Bluetooth: "bt_AA", Choice: ChooseUSB}, Sink{Bluetooth, "bt_AA"}},
	}
	for _, c := range cases {
		if got := Resolve(c.st); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
	if ChooseBT.Available(State{}) || !ChooseBT.Available(State{Bluetooth: "x"}) || !ThisOne.Available(State{}) {
		t.Error("Available")
	}
}

func TestOnSinkHearsEachMove(t *testing.T) {
	g := newRig(State{})
	var heard []Sink
	g.r.OnSink = func(s Sink) { heard = append(heard, s) }
	g.r.Open(48000)
	g.r.Write(make([]int16, 4))
	g.set(State{Wired: true}) // the same codec, another way out
	g.tick()
	g.r.Write(make([]int16, 4))
	g.set(State{Wired: true, Bluetooth: "bt", Choice: ChooseBT})
	g.tick()
	g.r.Write(make([]int16, 4))
	g.tick()
	g.r.Write(make([]int16, 4)) // nothing moved
	want := []Sink{{Speaker, "default"}, {Wired, "default"}, {Bluetooth, "bt"}}
	if len(heard) != len(want) {
		t.Fatalf("heard %+v", heard)
	}
	for i := range want {
		if heard[i] != want[i] {
			t.Fatalf("heard %+v, want %+v", heard, want)
		}
	}
	if g.devs["default"].closed != true {
		t.Fatal("the codec stayed open under the headset")
	}
}

type stallErr struct{}

func (stallErr) Error() string { return "stalled" }
func (stallErr) Stalled() bool { return true }

// stallDev stalls on its first write after opening, then plays.
type stallDev struct {
	dev
	stalls int
}

func (d *stallDev) Write(b []int16) error {
	if d.stalls > 0 {
		d.stalls--
		d.closed = true
		return stallErr{}
	}
	return d.dev.Write(b)
}

func TestAStalledDeviceIsOpenedAgain(t *testing.T) {
	sd := &stallDev{dev: dev{name: "bt"}, stalls: 1}
	r := New(func() State { return State{Bluetooth: "bt"} }, func(string) audio.Output { return sd }, nil)
	if err := r.Open(48000); err != nil {
		t.Fatal(err)
	}
	opens := sd.opens
	if err := r.Write(make([]int16, 4)); err != nil {
		t.Fatalf("a stall came through: %v", err)
	}
	if sd.opens != opens+1 || sd.writes != 1 {
		t.Fatalf("opens %d (was %d), writes %d", sd.opens, opens, sd.writes)
	}
}
