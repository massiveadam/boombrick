package peq

import (
	"math"
	"math/rand/v2"
	"testing"
)

var rates = []float64{44100, 48000, 96000}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestPeakGainAtCentre(t *testing.T) {
	for _, rate := range rates {
		for _, g := range []float64{-12, -3, 0.5, 6, 12} {
			for _, fc := range []float64{30, 1000, 15000} {
				c := Filter{On: true, Kind: Peak, Freq: fc, Gain: g, Q: 1.4}.Coefficients(rate)
				if got := c.Response(fc, rate); !near(got, g, 1e-9) {
					t.Errorf("rate %g fc %g gain %g: response at fc = %g", rate, fc, g, got)
				}
				// Far from the band it is flat.
				far := fc / 64
				if fc < 100 {
					far = fc * 64
				}
				if got := c.Response(far, rate); !near(got, 0, 0.05) {
					t.Errorf("rate %g fc %g gain %g: response at %g = %g, want ~0", rate, fc, g, far, got)
				}
			}
		}
	}
}

func TestShelves(t *testing.T) {
	for _, rate := range rates {
		ls := Filter{On: true, Kind: LowShelf, Freq: 105, Gain: 6.4, Q: 0.7}.Coefficients(rate)
		if got := ls.Response(5, rate); !near(got, 6.4, 0.05) {
			t.Errorf("low shelf deep bass = %g, want 6.4", got)
		}
		if got := ls.Response(105, rate); !near(got, 3.2, 1e-9) {
			t.Errorf("low shelf at fc = %g, want half the gain, 3.2", got)
		}
		if got := ls.Response(10000, rate); !near(got, 0, 0.01) {
			t.Errorf("low shelf treble = %g, want 0", got)
		}
		hs := Filter{On: true, Kind: HighShelf, Freq: 10000, Gain: -2.1, Q: 0.7}.Coefficients(rate)
		if got := hs.Response(10000, rate); !near(got, -1.05, 1e-9) {
			t.Errorf("high shelf at fc = %g, want -1.05", got)
		}
		if got := hs.Response(100, rate); !near(got, 0, 0.01) {
			t.Errorf("high shelf bass = %g, want 0", got)
		}
		if got := hs.Response(rate/2*0.999, rate); !near(got, -2.1, 0.15) {
			t.Errorf("high shelf at Nyquist = %g, want about -2.1", got)
		}
	}
}

func TestPassFilters(t *testing.T) {
	for _, rate := range rates {
		lp := Filter{On: true, Kind: LowPass, Freq: 2000, Q: DefaultQ}.Coefficients(rate)
		hp := Filter{On: true, Kind: HighPass, Freq: 2000, Q: DefaultQ}.Coefficients(rate)
		for _, c := range []Coeffs{lp, hp} {
			if got := c.Response(2000, rate); !near(got, -3.0103, 0.001) {
				t.Errorf("rate %g: Butterworth response at fc = %g, want -3.01", rate, got)
			}
		}
		if got := lp.Response(20, rate); !near(got, 0, 0.001) {
			t.Errorf("low pass at 20 Hz = %g", got)
		}
		if got := hp.Response(16000, rate); !near(got, 0, 0.1) {
			t.Errorf("high pass at 16 kHz = %g", got)
		}
		// 12 dB per octave well past the corner.
		if d := lp.Response(4000, rate) - lp.Response(8000, rate); !near(d, 12, 1.5) {
			t.Errorf("rate %g: low pass slope = %g dB/oct", rate, d)
		}
	}
}

func TestOffFilterIsIdentity(t *testing.T) {
	c := Filter{On: false, Kind: Peak, Freq: 1000, Gain: 12, Q: 1}.Coefficients(48000)
	if c != identity {
		t.Fatalf("off filter = %+v", c)
	}
}

// Every filter within the limits is stable, at every rate, including the
// extremes: very low frequencies with high Q, and frequencies at Nyquist.
func TestStabilityAcrossLimits(t *testing.T) {
	freqs := []float64{MinFreq, 15, 20, 40, 100, 1000, 10000, 20000, MaxFreq}
	qs := []float64{MinQ, 0.1, 0.5, DefaultQ, 2, 10, MaxQ}
	gains := []float64{-MaxGain, -12, 0, 12, MaxGain}
	for _, rate := range append(rates, 8000, 22050, 192000) {
		for k := Peak; k <= HighPass; k++ {
			for _, f := range freqs {
				for _, q := range qs {
					for _, g := range gains {
						c := Filter{On: true, Kind: k, Freq: f, Gain: g, Q: q}.Coefficients(rate)
						if !c.Stable() || bad(c.B0) || bad(c.A1) {
							t.Fatalf("unstable: rate %g %v f %g q %g g %g: %+v", rate, k, f, q, g, c)
						}
					}
				}
			}
		}
	}
}

func TestDesignClampsAboveNyquist(t *testing.T) {
	p := Profile{Filters: []Filter{{On: true, Kind: Peak, Freq: 21000, Gain: 6, Q: 1}}}
	d := p.Design(22050) // Nyquist 11025
	if len(d.Stages) != 1 || !d.Stages[0].Stable() {
		t.Fatalf("design = %+v", d)
	}
}

func TestValidate(t *testing.T) {
	good := Profile{Preamp: -6, Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Gain: 3, Q: 1}}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bads := []Profile{
		{Preamp: math.NaN()},
		{Preamp: 40},
		{Filters: []Filter{{Kind: Peak, Freq: 5, Q: 1}}},
		{Filters: []Filter{{Kind: Peak, Freq: 1000, Q: 0}}},
		{Filters: []Filter{{Kind: Peak, Freq: 1000, Q: 1, Gain: math.Inf(1)}}},
		{Filters: []Filter{{Kind: 9, Freq: 1000, Q: 1}}},
		{Filters: make([]Filter, MaxFilters+1)},
	}
	for i, p := range bads {
		if p.Validate() == nil {
			t.Errorf("case %d validated", i)
		}
	}
}

func TestHeadroomAndPeak(t *testing.T) {
	p := Profile{Filters: []Filter{
		{On: true, Kind: Peak, Freq: 3000, Gain: 7.5, Q: 2},
		{On: true, Kind: Peak, Freq: 100, Gain: -4, Q: 1},
	}}
	d := p.Design(48000)
	db, f := d.Peak()
	if !near(db, 7.5, 0.05) || !near(f, 3000, 60) {
		t.Fatalf("peak %g dB at %g Hz", db, f)
	}
	if h := p.Headroom(48000); !near(h, -db, 1e-9) {
		t.Fatalf("headroom %g", h)
	}
	p.Preamp = p.Headroom(48000)
	if db, _ := p.Design(48000).Peak(); !near(db, 0, 1e-6) {
		t.Fatalf("after headroom the peak is %g dB", db)
	}
	cut := Profile{Filters: []Filter{{On: true, Kind: Peak, Freq: 3000, Gain: -6, Q: 2}}}
	if h := cut.Headroom(48000); h != 0 {
		t.Fatalf("a cut needs no headroom, got %g", h)
	}
}

func TestFlat(t *testing.T) {
	if !(Profile{}).Flat() {
		t.Fatal("empty profile not flat")
	}
	if !(Profile{Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Q: 1}}}).Flat() {
		t.Fatal("zero-gain peak not flat")
	}
	if (Profile{Filters: []Filter{{On: true, Kind: HighPass, Freq: 30, Q: 0.7}}}).Flat() {
		t.Fatal("high pass counted as flat")
	}
	if (Profile{Preamp: -1}).Flat() {
		t.Fatal("preamp counted as flat")
	}
}

// sine fills n interleaved stereo frames with independent sines on L and R.
func sine(n int, rate, fl, fr, amp float64) []float32 {
	buf := make([]float32, 2*n)
	for i := range n {
		t := float64(i) / rate
		buf[2*i] = float32(amp * math.Sin(2*math.Pi*fl*t))
		if fr > 0 {
			buf[2*i+1] = float32(amp * math.Sin(2*math.Pi*fr*t))
		}
	}
	return buf
}

// rms is the RMS of channel ch over frames [from, to).
func rms(buf []float32, ch, from, to int) float64 {
	s := 0.0
	for i := from; i < to; i++ {
		v := float64(buf[2*i+ch])
		s += v * v
	}
	return math.Sqrt(s / float64(to-from))
}

var autoEqLike = Profile{Preamp: -6.1, Filters: []Filter{
	{On: true, Kind: LowShelf, Freq: 105, Gain: 6.4, Q: 0.70},
	{On: true, Kind: Peak, Freq: 8800, Gain: 5.1, Q: 1.42},
	{On: true, Kind: Peak, Freq: 118, Gain: -3.1, Q: 0.50},
	{On: true, Kind: Peak, Freq: 37, Gain: 0.7, Q: 3.96},
	{On: true, Kind: Peak, Freq: 3169, Gain: -1.7, Q: 3.89},
	{On: true, Kind: HighShelf, Freq: 10000, Gain: -2.1, Q: 0.70},
	{On: true, Kind: Peak, Freq: 1227, Gain: -1.2, Q: 2.53},
	{On: true, Kind: Peak, Freq: 2055, Gain: 1.2, Q: 3.23},
	{On: true, Kind: Peak, Freq: 587, Gain: 0.4, Q: 1.19},
	{On: true, Kind: Peak, Freq: 5332, Gain: -1.1, Q: 5.75},
}}

// The audio the processor actually produces matches the designed response:
// sines through it come out at the gain Response predicts, at every rate.
func TestMeasuredResponseMatchesDesign(t *testing.T) {
	for _, rate := range rates {
		d := autoEqLike.Design(rate)
		for _, f := range []float64{40, 105, 300, 1000, 3169, 8800, 15000} {
			p := NewProcessor()
			p.Set(autoEqLike, false)
			n := int(rate) // one second; the filters settle in the first half
			buf := sine(n, rate, f, 0, 0.25)
			in := rms(buf, 0, n/2, n)
			for i := 0; i < len(buf); i += 2048 {
				p.Process(buf[i:min(i+2048, len(buf))], int(rate))
			}
			got := 20 * math.Log10(rms(buf, 0, n/2, n)/in)
			if want := d.Response(f); !near(got, want, 0.02) {
				t.Errorf("rate %g, %g Hz: measured %.3f dB, designed %.3f dB", rate, f, got, want)
			}
		}
	}
}

// Left and right are filtered independently: each comes out as it would
// alone, and silence on one side stays silence.
func TestStereoChannelsIndependent(t *testing.T) {
	const rate = 48000
	n := 9000
	both := sine(n, rate, 440, 3000, 0.3)
	left := sine(n, rate, 440, 0, 0.3)
	right := make([]float32, 2*n)
	for i := range n {
		right[2*i+1] = both[2*i+1]
	}
	for _, b := range [][]float32{both, left, right} {
		p := NewProcessor()
		p.Set(autoEqLike, false)
		p.Process(b, rate)
	}
	for i := range n {
		if both[2*i] != left[2*i] || both[2*i+1] != right[2*i+1] {
			t.Fatalf("frame %d: channels interact", i)
		}
		if left[2*i+1] != 0 || right[2*i] != 0 {
			t.Fatalf("frame %d: a silent channel isn't silent", i)
		}
	}
}

// The result doesn't depend on how the audio is cut into buffers.
func TestChunkingInvariant(t *testing.T) {
	const rate = 44100
	n := 20000
	ref := sine(n, rate, 97, 5123, 0.4)
	got := append([]float32(nil), ref...)
	p := NewProcessor()
	p.Set(autoEqLike, false)
	p.Process(ref, rate)

	q := NewProcessor()
	q.Set(autoEqLike, false)
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < n; {
		k := min(n-i, 1+r.IntN(1500))
		q.Process(got[2*i:2*(i+k)], rate)
		i += k
	}
	for i := range ref {
		if ref[i] != got[i] {
			t.Fatalf("sample %d differs: %g vs %g", i, ref[i], got[i])
		}
	}
}

func TestBypassIsBitExact(t *testing.T) {
	buf := sine(4800, 48000, 1000, 2000, 0.5)
	orig := append([]float32(nil), buf...)
	p := NewProcessor()
	p.Set(autoEqLike, true)
	p.Process(buf, 48000)
	for i := range buf {
		if buf[i] != orig[i] {
			t.Fatalf("bypass changed sample %d", i)
		}
	}
	if p.Stats().Active {
		t.Fatal("bypassed processor reports active")
	}
}

// Switching bypass on and off while playing crossfades instead of jumping,
// and ends fully dry or fully wet.
func TestBypassToggleCrossfades(t *testing.T) {
	const rate = 48000
	boost := Profile{Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Gain: 12, Q: 1}}}
	p := NewProcessor()
	p.Set(boost, false)
	warm := sine(rate, rate, 1000, 1000, 0.1)
	p.Process(warm, rate)
	p.Set(boost, true)
	buf := sine(rate/10, rate, 1000, 1000, 0.1)
	dry := append([]float32(nil), buf...)
	p.Process(buf, rate)
	// No sample-to-sample jump bigger than the sine's own slope allows at 12 dB.
	maxStep := 2 * math.Pi * 1000 / rate * 0.1 * 4 * 1.1
	prev := warm[len(warm)-2]
	for i := 0; i < len(buf); i += 2 {
		if math.Abs(float64(buf[i]-prev)) > maxStep {
			t.Fatalf("frame %d jumps %g", i/2, buf[i]-prev)
		}
		prev = buf[i]
	}
	// After the 20 ms fade it is dry.
	for i := 2 * rate * 25 / 1000; i < len(buf); i++ {
		if buf[i] != dry[i] {
			t.Fatalf("sample %d not dry after the fade", i)
		}
	}
}

func TestClippingIsCounted(t *testing.T) {
	const rate = 48000
	boost := Profile{Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Gain: 6, Q: 1}}}
	p := NewProcessor()
	p.Set(boost, false)
	p.Process(sine(rate, rate, 1000, 1000, 0.9), rate)
	st := p.Stats()
	if st.Clipped == 0 || st.Peak < 1.5 {
		t.Fatalf("stats %+v: want clipping", st)
	}
	// With the suggested headroom the same sine doesn't clip.
	boost.Preamp = boost.Headroom(rate)
	q := NewProcessor()
	q.Set(boost, false)
	q.Process(sine(rate, rate, 1000, 1000, 0.9), rate)
	if st := q.Stats(); st.Clipped != 0 || st.Peak > 0.91 {
		t.Fatalf("with headroom: %+v", st)
	}
	q.ResetStats()
	if st := q.Stats(); st.Clipped != 0 || st.Peak != 0 {
		t.Fatalf("after reset: %+v", st)
	}
}

// A change of rate redesigns the filters for it.
func TestRateChangeRedesigns(t *testing.T) {
	p := NewProcessor()
	f := Profile{Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Gain: 9, Q: 2}}}
	p.Set(f, false)
	for _, rate := range []float64{44100, 96000, 48000} {
		n := int(rate)
		buf := sine(n, rate, 1000, 0, 0.1)
		p.Process(buf, int(rate))
		got := 20 * math.Log10(rms(buf, 0, n/2, n)/(0.1/math.Sqrt2))
		if !near(got, 9, 0.02) {
			t.Errorf("rate %g: %g dB at 1 kHz, want 9", rate, got)
		}
	}
}

// After the input goes silent the filters' memory decays to exactly zero:
// no denormals linger.
func TestSilenceDecaysToZero(t *testing.T) {
	p := NewProcessor()
	p.Set(Profile{Filters: []Filter{{On: true, Kind: LowShelf, Freq: 20, Gain: 12, Q: 2}}}, false)
	p.Process(sine(4800, 48000, 50, 50, 0.8), 48000)
	silence := make([]float32, 2*48000)
	for range 20 {
		clear(silence)
		p.Process(silence, 48000)
	}
	for s := range p.z {
		for ch := range 2 {
			if p.z[s][ch] != [2]float64{} {
				t.Fatalf("stage %d channel %d memory %v", s, ch, p.z[s][ch])
			}
		}
	}
}

// Changing the profile while playing ramps the preamp rather than stepping it.
func TestPreampRamps(t *testing.T) {
	p := NewProcessor()
	p.Set(Profile{Preamp: -0.0001, Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Gain: 0.0001, Q: 1}}}, false)
	dc := func(n int) []float32 {
		b := make([]float32, 2*n)
		for i := range b {
			b[i] = 0.5
		}
		return b
	}
	p.Process(dc(100), 48000)
	p.Set(Profile{Preamp: -12, Filters: []Filter{{On: true, Kind: Peak, Freq: 1000, Gain: 0.0001, Q: 1}}}, false)
	b := dc(1000)
	p.Process(b, 48000)
	for i := 2; i < len(b); i += 2 {
		if b[i] > b[i-2]+1e-6 {
			t.Fatalf("frame %d rises during a cut", i/2)
		}
	}
	if b[0] < 0.49 || b[len(b)-2] > 0.13 {
		t.Fatalf("ramp from %g to %g", b[0], b[len(b)-2])
	}
}

func BenchmarkProcess10Filters(b *testing.B) {
	p := NewProcessor()
	p.Set(autoEqLike, false)
	buf := sine(1024, 48000, 1000, 500, 0.3)
	b.SetBytes(int64(len(buf) * 4))
	b.ResetTimer()
	for range b.N {
		p.Process(buf, 48000)
	}
	// Report the share of one core that real-time playback at 48 kHz needs.
	perSec := float64(b.Elapsed().Nanoseconds()) / float64(b.N) * (48000.0 / 1024)
	b.ReportMetric(perSec/1e9*100, "%core@48k")
}

func BenchmarkProcess20Filters(b *testing.B) {
	p := NewProcessor()
	prof := autoEqLike.Clone()
	prof.Filters = append(prof.Filters, autoEqLike.Filters...)
	p.Set(prof, false)
	buf := sine(1024, 48000, 1000, 500, 0.3)
	b.ResetTimer()
	for range b.N {
		p.Process(buf, 48000)
	}
	perSec := float64(b.Elapsed().Nanoseconds()) / float64(b.N) * (48000.0 / 1024)
	b.ReportMetric(perSec/1e9*100, "%core@48k")
}
