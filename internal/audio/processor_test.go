package audio

import (
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/peq"
)

// tone is a Source of a stereo sine at amp.
type tone struct {
	rate     int
	freq     float64
	amp      float64
	n, pos   int64
	mu       sync.Mutex
	lastSeek int64
}

func (t *tone) SampleRate() int { return t.rate }
func (t *tone) Frames() int64   { return t.n }
func (t *tone) Close() error    { return nil }
func (t *tone) SeekFrame(f int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pos, t.lastSeek = f, f
	return nil
}

func (t *tone) Read(buf []float32) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := min(int64(len(buf)/2), t.n-t.pos)
	for i := range k {
		v := float32(t.amp * math.Sin(2*math.Pi*t.freq*float64(t.pos+i)/float64(t.rate)))
		buf[2*i], buf[2*i+1] = v, v
	}
	t.pos += k
	if t.pos >= t.n {
		return int(k), io.EOF
	}
	return int(k), nil
}

// recorder is an Output that keeps every sample written.
type recorder struct {
	mu   sync.Mutex
	pcm  []int16
	rate int
}

func (r *recorder) Open(rate int) error { r.mu.Lock(); r.rate = rate; r.mu.Unlock(); return nil }
func (r *recorder) Drop() (int, error)  { return 0, nil }
func (r *recorder) Close() error        { return nil }
func (r *recorder) Write(buf []int16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pcm = append(r.pcm, buf...)
	return nil
}

func (r *recorder) samples() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int16(nil), r.pcm...)
}

// playAll plays src to its end through an engine with proc and returns what
// reached the output.
func playAll(t *testing.T, src Source, proc Processor) []int16 {
	t.Helper()
	out := &recorder{}
	done := make(chan struct{})
	e := NewEngine(out, func(End) { close(done) })
	e.SetProcessor(proc)
	e.Play(src)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("playback didn't end")
	}
	e.Close()
	return out.samples()
}

func rmsOf(pcm []int16, ch int) float64 {
	s := 0.0
	n := 0
	for i := len(pcm) / 2; i+ch < len(pcm); i += 2 { // the second half: settled
		v := float64(pcm[i+ch]) / 32767
		s += v * v
		n++
	}
	return math.Sqrt(s / float64(n))
}

// The equaliser changes the samples that reach the output device, after the
// resampler: a 1 kHz tone at 44.1 kHz comes out 6 dB louder through a +6 dB
// peak at 1 kHz, at the engine's 48 kHz.
func TestProcessorShapesOutput(t *testing.T) {
	src := func() Source { return &tone{rate: 44100, freq: 1000, amp: 0.2, n: 44100} }
	dry := playAll(t, src(), nil)

	eq := peq.NewProcessor()
	eq.Set(peq.Profile{Filters: []peq.Filter{{On: true, Kind: peq.Peak, Freq: 1000, Gain: 6, Q: 1}}}, false)
	wet := playAll(t, src(), eq)

	if len(dry) != len(wet) || len(dry) < 2*47000 {
		t.Fatalf("lengths %d, %d", len(dry), len(wet))
	}
	for ch := range 2 {
		gain := 20 * math.Log10(rmsOf(wet, ch)/rmsOf(dry, ch))
		if math.Abs(gain-6) > 0.05 {
			t.Errorf("channel %d: EQ gain at the output %.3f dB, want 6", ch, gain)
		}
	}
	if st := eq.Stats(); !st.Active || st.Clipped != 0 {
		t.Errorf("stats %+v", st)
	}

	// Bypassed, the output is bit for bit what it is without the EQ.
	off := peq.NewProcessor()
	off.Set(peq.Profile{Filters: []peq.Filter{{On: true, Kind: peq.Peak, Freq: 1000, Gain: 6, Q: 1}}}, true)
	byp := playAll(t, src(), off)
	for i := range dry {
		if dry[i] != byp[i] {
			t.Fatalf("bypassed output differs at sample %d", i)
		}
	}
}

// counting is a Processor that counts its calls.
type counting struct {
	mu             sync.Mutex
	frames, resets int
	rates          map[int]bool
}

func (c *counting) Process(buf []float32, rate int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frames += len(buf) / 2
	if c.rates == nil {
		c.rates = map[int]bool{}
	}
	c.rates[rate] = true
}

func (c *counting) Reset() { c.mu.Lock(); c.resets++; c.mu.Unlock() }

func (c *counting) get() (frames, resets int, rates map[int]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames, c.resets, c.rates
}

// The processor sees every output frame at the output rate, is reset when
// playback jumps, and not when a queued source follows without a gap.
func TestProcessorResetsOnJumpsNotGapless(t *testing.T) {
	out := &recorder{}
	ends := make(chan End, 4)
	e := NewEngine(out, func(x End) { ends <- x })
	c := &counting{}
	e.SetProcessor(c)
	a := &tone{rate: 48000, freq: 440, amp: 0.1, n: 4800}
	b := &tone{rate: 44100, freq: 440, amp: 0.1, n: 4410}
	e.Play(a)
	e.Queue(b)
	<-ends // a hands over to b
	<-ends // b ends
	_, resets, rates := c.get()
	if resets != 1 { // the one switch to a
		t.Errorf("%d resets through a gapless hand-over, want 1", resets)
	}
	if len(rates) != 1 || !rates[OutputRate] {
		t.Errorf("processor saw rates %v", rates)
	}
	frames, _, _ := c.get()
	if want := len(out.samples()) / 2; frames != want {
		t.Errorf("processor saw %d frames, the output got %d", frames, want)
	}

	// Cued, paused, so the seek finds it loaded.
	s := &tone{rate: 48000, freq: 440, amp: 0.1, n: 48000}
	e.Cue(s, 0)
	e.SeekFrame(24000)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, r, _ := c.get(); r >= 3 { // the switch to s, then the seek
			break
		}
		if time.Now().After(deadline) {
			_, r, _ := c.get()
			t.Fatalf("%d resets after a play and a seek, want 3", r)
		}
		time.Sleep(time.Millisecond)
	}
	e.Close()
}
