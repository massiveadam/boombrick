// Package route sends the output to where the sound should go now, on a
// device whose outputs are separate ALSA devices: the codec (speaker and
// wired jack), a Bluetooth headset through BlueALSA, a USB DAC. It follows
// plorpOS's rules (src/audioout.c, MIT), so Boombrick sends sound where the
// RG SP's own launcher would:
//
//   - a USB DAC first, then a wired plug, then a Bluetooth headset, then the
//     speaker: a cable is a plainer wish than a headset that is merely on;
//   - when the Bluetooth headset or DAC that was playing goes away, playback
//     pauses rather than carrying on out of the speaker;
//   - the codec is held by one program at a time (BaseOS has no dmix), so
//     opening it waits up to a second for a game to let go, as plorpOS's
//     own player does.
package route

import (
	"errors"
	"sync"
	"time"

	"github.com/ujjwal313/boombrick/internal/audio"
)

// Kind is where a sink plays.
type Kind uint8

const (
	Speaker Kind = iota
	Wired
	Bluetooth
	USB
)

func (k Kind) String() string {
	return [...]string{"speaker", "wired", "Bluetooth", "USB DAC"}[k]
}

// Sink is a place to play: its kind and the ALSA device that reaches it.
type Sink struct {
	Kind Kind
	Name string // "default" for the codec
}

// lost reports whether s, which was playing, has gone from st: a headset or
// DAC no longer there (plorpOS's aout_pause_on_loss). The codec is never
// lost: unplugging a cable carries on out of the speaker, as plorpOS does.
func lost(s Sink, st State) bool {
	switch s.Kind {
	case Bluetooth:
		return st.Bluetooth != s.Name
	case USB:
		return st.USB != s.Name
	}
	return false
}

// State is what is plugged in and connected.
type State struct {
	USB       string // the DAC's ALSA name, or ""
	Wired     bool
	Bluetooth string // the headset's ALSA name, or ""
	Codec     string // the codec's name; "" for "default"
}

// Resolve is where the sound goes for st.
func Resolve(st State) Sink {
	codec := st.Codec
	if codec == "" {
		codec = "default"
	}
	switch {
	case st.USB != "":
		return Sink{USB, st.USB}
	case st.Wired:
		return Sink{Wired, codec}
	case st.Bluetooth != "":
		return Sink{Bluetooth, st.Bluetooth}
	}
	return Sink{Speaker, codec}
}

// ErrSinkLost is returned by Write when the headset or DAC that was playing
// went away. The engine pauses on it; Resume plays wherever is next.
var ErrSinkLost = errors.New("route: the output went away")

// Router is an audio.Output that plays on the sink State says, reopening as
// it changes.
type Router struct {
	state func() State
	open  func(name string) audio.Output
	log   func(msg string, args ...any)

	// Tunables, which tests shorten.
	BusyWait   time.Duration // how long Open waits for a busy device
	RetryEvery time.Duration
	CheckEvery time.Duration // how often Write looks at State
	now        func() time.Time
	sleep      func(time.Duration)

	mu      sync.Mutex
	cur     audio.Output
	sink    Sink
	rate    int
	checked time.Time
}

var _ audio.Output = (*Router)(nil)

// New returns a router reading state and opening devices with open. log may
// be nil.
func New(state func() State, open func(name string) audio.Output, log func(string, ...any)) *Router {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Router{state: state, open: open, log: log,
		BusyWait: time.Second, RetryEvery: 100 * time.Millisecond, CheckEvery: 500 * time.Millisecond,
		now: time.Now, sleep: time.Sleep}
}

// Sink is where the router plays now; the zero Sink before the first Open.
func (r *Router) Sink() Sink {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sink
}

type busy interface{ Busy() bool }

func isBusy(err error) bool {
	var b busy
	return errors.As(err, &b) && b.Busy()
}

// Open opens the sink State names now at rate, waiting for it while another
// program holds it.
func (r *Router) Open(rate int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openLocked(Resolve(r.state()), rate)
}

func (r *Router) openLocked(s Sink, rate int) error {
	if r.cur != nil && s.Name != r.sink.Name {
		r.cur.Close()
		r.cur = nil
	}
	if r.cur == nil {
		r.cur = r.open(s.Name)
		r.log("output", "to", s.Kind.String(), "device", s.Name)
	}
	r.sink, r.rate, r.checked = s, rate, r.now()
	err := r.cur.Open(rate)
	for waited := time.Duration(0); err != nil && isBusy(err) && waited < r.BusyWait; waited += r.RetryEvery {
		r.sleep(r.RetryEvery)
		err = r.cur.Open(rate)
	}
	return err
}

// Write plays buf. Every CheckEvery it looks at State: a better sink takes
// over, and the loss of the named sink playing stops playback.
func (r *Router) Write(buf []int16) error {
	r.mu.Lock()
	if r.cur == nil {
		r.mu.Unlock()
		return errors.New("route: write before open")
	}
	if now := r.now(); now.Sub(r.checked) >= r.CheckEvery {
		r.checked = now
		st := r.state()
		if s := Resolve(st); s.Name != r.sink.Name {
			if lost(r.sink, st) {
				r.log("output lost", "was", r.sink.Kind.String())
				r.cur.Close()
				r.cur = nil
				r.mu.Unlock()
				return ErrSinkLost
			}
			if err := r.openLocked(s, r.rate); err != nil {
				r.mu.Unlock()
				return err
			}
		}
	}
	cur := r.cur
	r.mu.Unlock()
	return cur.Write(buf)
}

// Drop drops what the device holds.
func (r *Router) Drop() (int, error) {
	r.mu.Lock()
	cur := r.cur
	r.mu.Unlock()
	if cur == nil {
		return 0, nil
	}
	return cur.Drop()
}

// Close closes the device; the next Open picks the sink again.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return nil
	}
	err := r.cur.Close()
	r.cur = nil
	return err
}
