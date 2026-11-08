// Package peq is a parametric equaliser: peaking, shelving and pass filters
// with a preamp, designed per the RBJ Audio EQ Cookbook and run as biquads in
// transposed direct form II with float64 state, one state per channel.
//
// A Profile describes the EQ; Design turns it into coefficients for a sample
// rate; a Processor runs it over interleaved stereo float32 audio and can be
// handed a new Profile from another goroutine while it runs.
package peq

import (
	"errors"
	"fmt"
	"math"
)

// Kind is a filter's shape.
type Kind uint8

const (
	Peak      Kind = iota // peaking (bell) filter: Gain at Freq, width from Q
	LowShelf              // shelf below Freq; Freq is the midpoint of the slope
	HighShelf             // shelf above Freq
	LowPass               // 12 dB/oct low-pass; Gain ignored
	HighPass              // 12 dB/oct high-pass; Gain ignored
)

func (k Kind) String() string {
	switch k {
	case Peak:
		return "Peak"
	case LowShelf:
		return "Low shelf"
	case HighShelf:
		return "High shelf"
	case LowPass:
		return "Low pass"
	case HighPass:
		return "High pass"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// Limits on a filter's parameters. Values outside them are rejected by
// Validate and clamped by Design.
const (
	MaxFilters = 20
	MinFreq    = 10.0
	MaxFreq    = 22000.0
	MinQ       = 0.05
	MaxQ       = 30.0
	MaxGain    = 30.0 // dB, either way
	MaxPreamp  = 30.0 // dB, either way
	// nyquistMargin keeps a filter's frequency below this fraction of the
	// sample rate, where the bilinear transform still behaves.
	nyquistMargin = 0.49
)

// DefaultQ is a Butterworth Q, used when a preset gives none.
const DefaultQ = 1 / math.Sqrt2

// Filter is one band.
type Filter struct {
	On   bool
	Kind Kind
	Freq float64 // Hz
	Gain float64 // dB; unused by LowPass and HighPass
	Q    float64
}

// Profile is a whole EQ: a preamp and up to MaxFilters filters, in order.
type Profile struct {
	Name    string
	Preamp  float64 // dB, applied before the filters
	Filters []Filter
}

// Clone returns a deep copy.
func (p Profile) Clone() Profile {
	p.Filters = append([]Filter(nil), p.Filters...)
	return p
}

// Flat reports whether the profile changes nothing: no preamp and no filter
// that is on and does something.
func (p Profile) Flat() bool {
	if p.Preamp != 0 {
		return false
	}
	for _, f := range p.Filters {
		if f.On && (f.Kind == LowPass || f.Kind == HighPass || f.Gain != 0) {
			return false
		}
	}
	return true
}

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("peq: invalid profile")

// Validate reports the first parameter out of range.
func (p Profile) Validate() error {
	if len(p.Filters) > MaxFilters {
		return fmt.Errorf("%w: %d filters, at most %d", ErrInvalid, len(p.Filters), MaxFilters)
	}
	if bad(p.Preamp) || math.Abs(p.Preamp) > MaxPreamp {
		return fmt.Errorf("%w: preamp %g dB", ErrInvalid, p.Preamp)
	}
	for i, f := range p.Filters {
		switch {
		case f.Kind > HighPass:
			return fmt.Errorf("%w: filter %d: unknown kind %d", ErrInvalid, i+1, f.Kind)
		case bad(f.Freq) || f.Freq < MinFreq || f.Freq > MaxFreq:
			return fmt.Errorf("%w: filter %d: frequency %g Hz", ErrInvalid, i+1, f.Freq)
		case bad(f.Q) || f.Q < MinQ || f.Q > MaxQ:
			return fmt.Errorf("%w: filter %d: Q %g", ErrInvalid, i+1, f.Q)
		case bad(f.Gain) || math.Abs(f.Gain) > MaxGain:
			return fmt.Errorf("%w: filter %d: gain %g dB", ErrInvalid, i+1, f.Gain)
		}
	}
	return nil
}

func bad(x float64) bool { return math.IsNaN(x) || math.IsInf(x, 0) }

// Coeffs are a biquad's coefficients normalised by a0:
// y[n] = b0 x[n] + b1 x[n-1] + b2 x[n-2] - a1 y[n-1] - a2 y[n-2].
type Coeffs struct {
	B0, B1, B2, A1, A2 float64
}

// identity passes audio through unchanged.
var identity = Coeffs{B0: 1}

// Coefficients designs f for rate Hz, clamping its parameters into range and
// its frequency below Nyquist. A filter that is off is the identity.
func (f Filter) Coefficients(rate float64) Coeffs {
	if !f.On {
		return identity
	}
	freq := clamp(f.Freq, MinFreq, math.Min(MaxFreq, nyquistMargin*rate))
	q := clamp(f.Q, MinQ, MaxQ)
	gain := clamp(f.Gain, -MaxGain, MaxGain)
	if bad(freq) || bad(q) || bad(gain) {
		return identity
	}
	w0 := 2 * math.Pi * freq / rate
	cw, sw := math.Cos(w0), math.Sin(w0)
	alpha := sw / (2 * q)
	A := math.Pow(10, gain/40)
	var b0, b1, b2, a0, a1, a2 float64
	switch f.Kind {
	case Peak:
		b0, b1, b2 = 1+alpha*A, -2*cw, 1-alpha*A
		a0, a1, a2 = 1+alpha/A, -2*cw, 1-alpha/A
	case LowShelf:
		sa := 2 * math.Sqrt(A) * alpha
		b0 = A * ((A + 1) - (A-1)*cw + sa)
		b1 = 2 * A * ((A - 1) - (A+1)*cw)
		b2 = A * ((A + 1) - (A-1)*cw - sa)
		a0 = (A + 1) + (A-1)*cw + sa
		a1 = -2 * ((A - 1) + (A+1)*cw)
		a2 = (A + 1) + (A-1)*cw - sa
	case HighShelf:
		sa := 2 * math.Sqrt(A) * alpha
		b0 = A * ((A + 1) + (A-1)*cw + sa)
		b1 = -2 * A * ((A - 1) + (A+1)*cw)
		b2 = A * ((A + 1) + (A-1)*cw - sa)
		a0 = (A + 1) - (A-1)*cw + sa
		a1 = 2 * ((A - 1) - (A+1)*cw)
		a2 = (A + 1) - (A-1)*cw - sa
	case LowPass:
		b0, b1, b2 = (1-cw)/2, 1-cw, (1-cw)/2
		a0, a1, a2 = 1+alpha, -2*cw, 1-alpha
	case HighPass:
		b0, b1, b2 = (1+cw)/2, -(1 + cw), (1+cw)/2
		a0, a1, a2 = 1+alpha, -2*cw, 1-alpha
	default:
		return identity
	}
	return Coeffs{B0: b0 / a0, B1: b1 / a0, B2: b2 / a0, A1: a1 / a0, A2: a2 / a0}
}

// Stable reports whether the biquad's poles lie inside the unit circle, the
// stability triangle: |a2| < 1 and |a1| < 1 + a2.
func (c Coeffs) Stable() bool { return math.Abs(c.A2) < 1 && math.Abs(c.A1) < 1+c.A2 }

// Response is the biquad's gain at freq Hz for rate Hz, in dB.
func (c Coeffs) Response(freq, rate float64) float64 {
	w := 2 * math.Pi * freq / rate
	// H(z) at z = e^{jw}: (b0 + b1 z^-1 + b2 z^-2) / (1 + a1 z^-1 + a2 z^-2).
	c1, s1 := math.Cos(w), -math.Sin(w)
	c2, s2 := math.Cos(2*w), -math.Sin(2*w)
	nr, ni := c.B0+c.B1*c1+c.B2*c2, c.B1*s1+c.B2*s2
	dr, di := 1+c.A1*c1+c.A2*c2, c.A1*s1+c.A2*s2
	return 10 * math.Log10((nr*nr+ni*ni)/(dr*dr+di*di))
}

// Design is a profile's coefficients for one sample rate.
type Design struct {
	Rate   float64
	Gain   float64 // the preamp as a linear factor
	Stages []Coeffs
}

// Design designs p for rate Hz. Filters that are off are left out.
func (p Profile) Design(rate float64) Design {
	d := Design{Rate: rate, Gain: math.Pow(10, clamp(p.Preamp, -MaxPreamp, MaxPreamp)/20)}
	if bad(d.Gain) {
		d.Gain = 1
	}
	for i, f := range p.Filters {
		if i >= MaxFilters {
			break
		}
		if !f.On {
			continue
		}
		c := f.Coefficients(rate)
		if !c.Stable() {
			continue // never expected from the cookbook within the limits; never run one
		}
		d.Stages = append(d.Stages, c)
	}
	return d
}

// Response is the whole profile's gain at freq Hz, preamp included, in dB.
func (d Design) Response(freq float64) float64 {
	db := 20 * math.Log10(d.Gain)
	for _, c := range d.Stages {
		db += c.Response(freq, d.Rate)
	}
	return db
}

// Peak is the highest gain of the response between 20 Hz and 20 kHz (or
// Nyquist), preamp included, and where it is: a positive peak means a
// full-scale sine there would clip.
func (d Design) Peak() (db, freq float64) {
	db = math.Inf(-1)
	hi := math.Min(20000, d.Rate/2*0.999)
	const steps = 480 // 1/24 octave over ten octaves, refined below
	for i := 0; i <= steps; i++ {
		f := 20 * math.Pow(hi/20, float64(i)/steps)
		if g := d.Response(f); g > db {
			db, freq = g, f
		}
	}
	// Refine around the coarse peak by golden-section search in log frequency.
	lo, up := math.Log(freq/math.Pow(hi/20, 1.0/steps)), math.Log(freq*math.Pow(hi/20, 1.0/steps))
	lo, up = math.Max(lo, math.Log(20)), math.Min(up, math.Log(hi))
	const g = 0.6180339887498949
	for range 40 {
		a, b := up-g*(up-lo), lo+g*(up-lo)
		if d.Response(math.Exp(a)) > d.Response(math.Exp(b)) {
			up = b
		} else {
			lo = a
		}
	}
	if f := math.Exp((lo + up) / 2); d.Response(f) > db {
		db, freq = d.Response(f), f
	}
	return db, freq
}

// Headroom is the preamp change, in dB, that brings the response's peak to
// 0 dB: negative when the profile boosts above unity, 0 when it doesn't.
func (p Profile) Headroom(rate float64) float64 {
	peak, _ := p.Design(rate).Peak()
	if peak <= 0 {
		return 0
	}
	return -peak
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }
