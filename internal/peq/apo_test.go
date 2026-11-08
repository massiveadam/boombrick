package peq

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The exact format AutoEq writes as ParametricEQ.txt (values from its
// published Sennheiser HD 650 result, used only as parser input).
const autoEqText = "Preamp: -6.1 dB\r\n" +
	"Filter 1: ON LSC Fc 105 Hz Gain 6.4 dB Q 0.70\r\n" +
	"Filter 2: ON PK Fc 8800 Hz Gain 5.1 dB Q 1.42\n" +
	"Filter 3: ON PK Fc 118 Hz Gain -3.1 dB Q 0.50\n" +
	"Filter 4: ON PK Fc 37 Hz Gain 0.7 dB Q 3.96\n" +
	"Filter 5: ON PK Fc 3169 Hz Gain -1.7 dB Q 3.89\n" +
	"Filter 6: ON HSC Fc 10000 Hz Gain -2.1 dB Q 0.70\n" +
	"Filter 7: ON PK Fc 1227 Hz Gain -1.2 dB Q 2.53\n" +
	"Filter 8: ON PK Fc 2055 Hz Gain 1.2 dB Q 3.23\n" +
	"Filter 9: ON PK Fc 587 Hz Gain 0.4 dB Q 1.19\n" +
	"Filter 10: ON PK Fc 5332 Hz Gain -1.1 dB Q 5.75\n"

func TestParseAutoEq(t *testing.T) {
	p, warns, err := ParseAPO(strings.NewReader("\xef\xbb\xbf" + autoEqText))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	want := autoEqLike
	if p.Preamp != want.Preamp || len(p.Filters) != len(want.Filters) {
		t.Fatalf("got %+v", p)
	}
	for i := range p.Filters {
		if p.Filters[i] != want.Filters[i] {
			t.Errorf("filter %d = %+v, want %+v", i+1, p.Filters[i], want.Filters[i])
		}
	}
}

func TestParseVariants(t *testing.T) {
	in := `# a comment
Device: Headphones
Preamp: -3 dB
Preamp: -1.5 dB
Filter: ON PEQ Fc 1000 Hz Gain 2 dB Q 1
Filter  2:  OFF  PK  Fc 2000 Hz Gain 3 dB Q 2
Filter 3: ON Modal Fc 100 Hz Gain 3.0 dB Q 5.41 T60 target 100 ms
Filter 4: ON PK Fc 500 Hz Gain -2 dB BW Oct 1
Filter 5: ON LSC 12 dB Fc 80 Hz Gain 4 dB
Filter 6: ON HS Fc 9000 Hz Gain -2 dB
Filter 7: ON LP Fc 18000 Hz
Filter 8: ON HPQ Fc 25 Hz Q 0.5
Filter 9: ON LS 6dB Fc 100 Hz Gain 3 dB
Filter 10: ON NO Fc 60 Hz
Filter 11: ON PK Fc 1000 Hz Gain 3 dB
GraphicEQ: 20 0; 1000 3
Channel: L
Filter 12: ON PK Fc 300 Hz Gain 5 dB Q 1
Channel: all
Filter 13: ON PK Fc 12000 Hz Gain 1 dB Q 3 # trailing comment
nonsense line
`
	p, warns, err := ParseAPO(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if p.Preamp != -4.5 {
		t.Errorf("preamps sum to %g, want -4.5", p.Preamp)
	}
	got := map[float64]Filter{}
	for _, f := range p.Filters {
		got[f.Freq] = f
	}
	check := func(freq float64, want Filter) {
		t.Helper()
		f, ok := got[freq]
		if !ok {
			t.Errorf("no filter at %g Hz", freq)
			return
		}
		if f.On != want.On || f.Kind != want.Kind || !near(f.Gain, want.Gain, 1e-9) || !near(f.Q, want.Q, 1e-3) {
			t.Errorf("%g Hz: got %+v, want %+v", freq, f, want)
		}
	}
	check(1000, Filter{On: true, Kind: Peak, Gain: 2, Q: 1})
	check(2000, Filter{On: false, Kind: Peak, Gain: 3, Q: 2})
	check(100, Filter{On: true, Kind: Peak, Gain: 3, Q: 5.41})
	check(500, Filter{On: true, Kind: Peak, Gain: -2, Q: 1.4142}) // 1 octave
	check(9000, Filter{On: true, Kind: HighShelf, Gain: -2, Q: DefaultQ})
	check(18000, Filter{On: true, Kind: LowPass, Q: DefaultQ})
	check(25, Filter{On: true, Kind: HighPass, Q: 0.5})
	check(12000, Filter{On: true, Kind: Peak, Gain: 1, Q: 3})
	// A 12 dB/oct shelf is S = 1: its Q follows from the gain.
	if f := got[80]; f.Kind != LowShelf || !near(f.Q, qFromSlope(12, 4), 1e-12) || !near(qFromSlope(12, 0), DefaultQ, 1e-12) {
		t.Errorf("slope shelf %+v", f)
	}
	if _, ok := got[300]; ok {
		t.Error("left-only filter applied to both channels")
	}
	if _, ok := got[60]; ok {
		t.Error("notch accepted")
	}
	if len(p.Filters) != 9 {
		t.Errorf("%d filters, want 9: %+v", len(p.Filters), p.Filters)
	}
	wantWarn := []string{"Device", "Q 0.7071 assumed", "corner-frequency", "NO isn't supported",
		"no Q", "GraphicEQ", "some channels only", "not a command"}
	all := fmt.Sprint(warns)
	for _, w := range wantWarn {
		if !strings.Contains(all, w) {
			t.Errorf("no warning mentioning %q in %s", w, all)
		}
	}
}

func TestParseTooManyFilters(t *testing.T) {
	var b strings.Builder
	for i := range MaxFilters + 1 {
		fmt.Fprintf(&b, "Filter %d: ON PK Fc %d Hz Gain 1 dB Q 1\n", i+1, 100+i*100)
	}
	if _, _, err := ParseAPO(strings.NewReader(b.String())); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsOutOfRange(t *testing.T) {
	p, warns, err := ParseAPO(strings.NewReader("Filter: ON PK Fc 1000 Hz Gain 45 dB Q 1\nFilter: ON PK Fc 1000 Hz Gain 1 dB Q 0\n"))
	if err != nil || len(p.Filters) != 0 || len(warns) != 2 {
		t.Fatalf("p %+v warns %v err %v", p, warns, err)
	}
}

func TestFormatRoundTrip(t *testing.T) {
	in := autoEqLike.Clone()
	in.Name = "HD 650"
	in.Filters = append(in.Filters,
		Filter{On: false, Kind: Peak, Freq: 440, Gain: -0.25, Q: 9},
		Filter{On: true, Kind: HighPass, Freq: 20, Q: 0.5},
		Filter{On: true, Kind: LowPass, Freq: 19500, Q: DefaultQ})
	text := FormatAPO(in)
	out, warns, err := ParseAPO(strings.NewReader(text))
	if err != nil || len(warns) != 0 {
		t.Fatalf("err %v warns %v\n%s", err, warns, text)
	}
	if out.Preamp != in.Preamp || len(out.Filters) != len(in.Filters) {
		t.Fatalf("round trip:\n%s\n%+v", text, out)
	}
	for i := range in.Filters {
		if out.Filters[i] != in.Filters[i] {
			t.Errorf("filter %d: %+v -> %+v", i+1, in.Filters[i], out.Filters[i])
		}
	}
	if !strings.HasPrefix(text, "# Name: HD 650\n") {
		t.Errorf("no name comment:\n%s", text)
	}
}

func FuzzParseAPO(f *testing.F) {
	f.Add(autoEqText)
	f.Add("Filter: ON LSC 12 dB Fc 80 Hz Gain 4 dB\nChannel: L R\nPreamp: 3")
	f.Fuzz(func(t *testing.T, s string) {
		p, _, err := ParseAPO(strings.NewReader(s))
		if err != nil {
			return
		}
		for _, fl := range p.Filters {
			if (Profile{Filters: []Filter{fl}}).Validate() != nil {
				t.Fatalf("parsed an invalid filter %+v", fl)
			}
			if c := fl.Coefficients(48000); !c.Stable() {
				t.Fatalf("parsed an unstable filter %+v", fl)
			}
		}
	})
}
