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
