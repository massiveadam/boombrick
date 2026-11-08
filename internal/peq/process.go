package peq

import (
	"math"
	"sync/atomic"
)

// crossfadeSeconds is how long switching bypass on or off takes: the wet and
// dry signals are crossfaded so the switch doesn't click.
const crossfadeSeconds = 0.02

// setting is what Set hands the audio goroutine.
type setting struct {
	profile Profile
	bypass  bool
}

// Stats are what the processor has seen since it was made.
type Stats struct {
	Clipped uint64  // output samples that left [-1, 1] after the EQ
	Peak    float32 // the loudest output sample, absolute
	Active  bool    // the EQ is running (not bypassed and not flat)
}

// Processor runs a Profile over interleaved stereo float32 audio. Set may be
// called from any goroutine; Process and Reset only from the one goroutine
// that plays the audio. Process allocates only when the buffer grows or the
// profile changes.
type Processor struct {
	pending atomic.Pointer[setting]

	// Owned by the audio goroutine.
	rate    float64
	cur     setting
	design  Design
	z       [][2][2]float64 // per stage, per channel: z1, z2
	gain    float64         // the preamp gain in effect, ramped towards design.Gain
	wet     float64         // 1 = EQ fully in, 0 = bypassed; ramps on a change
	scratch []float64
	started bool // Process has run: later changes ramp instead of jumping

	clipped atomic.Uint64
	peak    atomic.Uint32 // float32 bits
	active  atomic.Bool
}

// NewProcessor returns a processor that passes audio through until Set.
func NewProcessor() *Processor {
	p := &Processor{gain: 1}
	p.pending.Store(&setting{bypass: true})
	return p
}

// Set hands the processor a new profile and bypass state, taking effect at the
// next Process. The profile is copied.
func (p *Processor) Set(prof Profile, bypass bool) {
	p.pending.Store(&setting{profile: prof.Clone(), bypass: bypass})
}

// Stats reports the clipping counted and the peak seen.
func (p *Processor) Stats() Stats {
	return Stats{Clipped: p.clipped.Load(), Peak: math.Float32frombits(p.peak.Load()), Active: p.active.Load()}
}

// ResetStats zeroes the clip count and the peak.
func (p *Processor) ResetStats() {
	p.clipped.Store(0)
	p.peak.Store(0)
}

// Reset clears the filters' memory, as after a seek or a new track that
// doesn't follow on, so nothing of the old audio rings into the new.
func (p *Processor) Reset() {
	for i := range p.z {
		p.z[i] = [2][2]float64{}
	}
}

// Process equalises buf, interleaved stereo frames at rate Hz, in place.
func (p *Processor) Process(buf []float32, rate int) {
	if s := p.pending.Swap(nil); s != nil {
		p.apply(*s, float64(rate))
	} else if float64(rate) != p.rate {
		p.apply(p.cur, float64(rate))
	}
	p.started = true
	target := 1.0
	if p.cur.bypass || p.cur.profile.Flat() {
		target = 0
	}
	p.active.Store(target == 1)
	if target == 0 && p.wet == 0 {
		p.measure(buf) // bypassed: untouched, but still watched for clipping
		return
	}
	n := len(buf) / 2
	if cap(p.scratch) < 2*n {
		p.scratch = make([]float64, 2*n)
	}
	x := p.scratch[:2*n]

	// Preamp, ramped linearly across the buffer from the gain in effect.
	g0, g1 := p.gain, p.design.Gain
	step := 0.0
	if n > 0 {
		step = (g1 - g0) / float64(n)
	}
	for i := range n {
		g := g0 + step*float64(i+1)
		x[2*i] = float64(buf[2*i]) * g
		x[2*i+1] = float64(buf[2*i+1]) * g
	}
	p.gain = g1

	for s := range p.design.Stages {
		c := &p.design.Stages[s]
		b0, b1, b2, a1, a2 := c.B0, c.B1, c.B2, c.A1, c.A2
		zl, zr := &p.z[s][0], &p.z[s][1]
		l1, l2, r1, r2 := zl[0], zl[1], zr[0], zr[1]
		for i := 0; i < 2*n; i += 2 {
			// Transposed direct form II, one channel after the other.
			in := x[i]
			out := b0*in + l1
			l1 = b1*in - a1*out + l2
			l2 = b2*in - a2*out
			x[i] = out

			in = x[i+1]
			out = b0*in + r1
			r1 = b1*in - a1*out + r2
			r2 = b2*in - a2*out
			x[i+1] = out
		}
		zl[0], zl[1], zr[0], zr[1] = flush(l1), flush(l2), flush(r1), flush(r2)
	}

	// Write back, crossfading against the dry signal while bypass changes.
	if p.wet == target {
		for i := range x {
			buf[i] = float32(x[i])
		}
	} else {
		fade := 1 / (crossfadeSeconds * p.rate)
		w := p.wet
		for i := range n {
			if w < target {
				w = math.Min(target, w+fade)
			} else if w > target {
				w = math.Max(target, w-fade)
			}
			buf[2*i] = float32(w*x[2*i] + (1-w)*float64(buf[2*i]))
			buf[2*i+1] = float32(w*x[2*i+1] + (1-w)*float64(buf[2*i+1]))
		}
		p.wet = w
		if w == 0 {
			p.Reset() // fully bypassed: start clean when switched back on
		}
	}
	p.measure(buf)
}

// apply switches to s at rate. The filters' memory is kept for the stages
// that remain, so a small change to a band doesn't click.
func (p *Processor) apply(s setting, rate float64) {
	if rate <= 0 {
		rate = 48000
	}
	p.cur, p.rate = s, rate
	p.design = s.profile.Design(rate)
	if !p.started || p.wet == 0 {
		// Nothing of the old setting is being heard: start at the new one.
		p.gain = p.design.Gain
	}
	if !p.started {
		p.wet = 0
		if !s.bypass && !s.profile.Flat() {
			p.wet = 1
		}
	}
	if len(p.z) != len(p.design.Stages) {
		z := make([][2][2]float64, len(p.design.Stages))
		copy(z, p.z)
		p.z = z
	}
}

// measure counts samples that will clip and keeps the peak.
func (p *Processor) measure(buf []float32) {
	var clipped uint64
	var peak float32
	for _, v := range buf {
		a := v
		if a < 0 {
			a = -a
		}
		if a > peak {
			peak = a
		}
		if a > 1 {
			clipped++
		}
	}
	if clipped > 0 {
		p.clipped.Add(clipped)
	}
	for {
		old := p.peak.Load()
		if math.Float32frombits(old) >= peak || p.peak.CompareAndSwap(old, math.Float32bits(peak)) {
			break
		}
	}
}

// flush zeroes values small enough to be denormal, which some CPUs process
// slowly, as an IIR filter's memory decays towards them in silence.
func flush(v float64) float64 {
	if v > -1e-30 && v < 1e-30 {
		return 0
	}
	return v
}
