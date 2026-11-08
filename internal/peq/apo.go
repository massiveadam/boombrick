package peq

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Warning is a line of a preset that was read only in part, or not at all.
type Warning struct {
	Line int
	Text string
}

func (w Warning) String() string { return fmt.Sprintf("line %d: %s", w.Line, w.Text) }

var filterCmd = regexp.MustCompile(`^filter\s*\d*$`)

// cornerSlope matches the fixed-slope, corner-frequency shelf forms "6dB" and
// "12dB" that follow LS or HS, which are a different design from the
// centre-frequency shelves this package implements.
var cornerSlope = regexp.MustCompile(`^(?i)(6|12)db$`)

// ParseAPO reads an Equalizer APO configuration, such as an AutoEq
// ParametricEQ.txt, into a profile.
//
// It reads Preamp (summed, as APO does) and Filter lines of types PK, PEQ,
// Modal, LP, LPQ, HP, HPQ, LS, LSC, HS and HSC, with Fc, Gain, and Q or
// "BW Oct". That covers AutoEq's ParametricEQ.txt and squiglink's "Export"
// (its "<model> Filters.txt", which writes LSC and HSC); squiglink's own
// LSQ and HSQ are read as LSC and HSC. Shelves take a Q or a slope in dB per octave ("LSC 12 dB");
// with neither, a Q of 0.7071 is assumed and a warning given. Filters
// switched OFF are kept, off.
//
// What it can't apply faithfully it leaves out, with a warning: filter types
// BP, NO, AP and IIR, the corner-frequency shelves "LS 6dB" and "LS 12dB",
// GraphicEQ, Convolution and the other commands, and any filters that a
// Channel line limits to some channels only. An error is returned only when
// the input can't be read or holds more than MaxFilters filters.
func ParseAPO(r io.Reader) (Profile, []Warning, error) {
	var p Profile
	var warns []Warning
	warn := func(line int, f string, a ...any) {
		warns = append(warns, Warning{Line: line, Text: fmt.Sprintf(f, a...)})
	}
	allChannels := true
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if n == 1 {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		cmd, rest, ok := strings.Cut(line, ":")
		if !ok {
			warn(n, "not a command, ignored")
			continue
		}
		cmd = strings.ToLower(strings.TrimSpace(cmd))
		rest = strings.TrimSpace(rest)
		switch {
		case cmd == "preamp":
			db, err := parseDB(rest)
			if err != nil {
				warn(n, "preamp %q unreadable, ignored", rest)
				continue
			}
			if !allChannels {
				warn(n, "preamp for some channels only, ignored")
				continue
			}
			p.Preamp += db
		case filterCmd.MatchString(cmd):
			if !allChannels {
				warn(n, "filter for some channels only, ignored: per-channel EQ isn't supported")
				continue
			}
			f, msg, ok := parseFilter(rest)
			if msg != "" {
				warn(n, "%s", msg)
			}
			if ok {
				p.Filters = append(p.Filters, f)
			}
		case cmd == "channel":
			allChannels = coversStereo(rest)
			if !allChannels {
				warn(n, "Channel %q selects some channels only; the filters after it are ignored", rest)
			}
		case cmd == "device":
			warn(n, "Device ignored: the profile applies to every output")
		default:
			warn(n, "%s isn't supported, ignored", strings.TrimSpace(strings.SplitN(line, ":", 2)[0]))
		}
	}
	if err := sc.Err(); err != nil {
		return Profile{}, warns, err
	}
	if len(p.Filters) > MaxFilters {
		return Profile{}, warns, fmt.Errorf("%w: %d filters, at most %d", ErrInvalid, len(p.Filters), MaxFilters)
	}
	return p, warns, nil
}

// coversStereo reports whether a Channel selection includes both stereo
// channels.
func coversStereo(sel string) bool {
	l, r := false, false
	for _, f := range strings.Fields(strings.ToUpper(sel)) {
		switch f {
		case "ALL":
			return true
		case "L", "1":
			l = true
		case "R", "2":
			r = true
		}
	}
	return l && r
}

// parseDB reads "-6.1 dB" or "-6.1".
func parseDB(s string) (float64, error) {
	f := strings.Fields(s)
	if len(f) == 0 || len(f) > 2 || len(f) == 2 && !strings.EqualFold(f[1], "db") {
		return 0, fmt.Errorf("bad value %q", s)
	}
	return parseNum(f[0])
}

func parseNum(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || bad(v) {
		return 0, fmt.Errorf("bad number %q", s)
	}
	return v, nil
}

// parseFilter reads what follows "Filter n:". It returns the filter, a
// warning or "", and whether the filter is to be kept.
func parseFilter(s string) (Filter, string, bool) {
	tok := strings.Fields(s)
	if len(tok) < 2 {
		return Filter{}, "filter incomplete, ignored", false
	}
	var f Filter
	switch strings.ToUpper(tok[0]) {
	case "ON":
		f.On = true
	case "OFF":
	default:
		return Filter{}, fmt.Sprintf("filter state %q isn't ON or OFF, ignored", tok[0]), false
	}
	typ := strings.ToUpper(tok[1])
	tok = tok[2:]
	shelf := false
	switch typ {
	case "PK", "PEQ", "MODAL":
		f.Kind = Peak
	case "LP", "LPQ":
		f.Kind = LowPass
	case "HP", "HPQ":
		f.Kind = HighPass
	case "LS", "LSC", "LSQ": // LSQ, HSQ: squiglink's own names for LSC, HSC
		f.Kind, shelf = LowShelf, true
	case "HS", "HSC", "HSQ":
		f.Kind, shelf = HighShelf, true
	default:
		return Filter{}, fmt.Sprintf("filter type %s isn't supported, ignored", typ), false
	}
	slope := 0.0
	if shelf && len(tok) > 0 {
		if cornerSlope.MatchString(tok[0]) && (typ == "LS" || typ == "HS") {
			return Filter{}, fmt.Sprintf("%s %s (corner-frequency shelf) isn't supported, ignored", typ, tok[0]), false
		}
		// "LSC 12 dB": a slope in dB per octave before the parameters.
		if v, err := parseNum(tok[0]); err == nil && len(tok) > 1 && strings.EqualFold(tok[1], "db") {
			slope, tok = v, tok[2:]
		}
	}
	var hasFc, hasGain, hasQ, hasBW bool
	var bw float64
	for i := 0; i < len(tok); i++ {
		key := strings.ToUpper(tok[i])
		next := func() (float64, bool) {
			if i+1 >= len(tok) {
				return 0, false
			}
			v, err := parseNum(tok[i+1])
			if err != nil {
				return 0, false
			}
			i++
			return v, true
		}
		var ok bool
		switch key {
		case "FC":
			f.Freq, ok = next()
			hasFc = ok
			if ok && i+1 < len(tok) && strings.EqualFold(tok[i+1], "hz") {
				i++
			}
		case "GAIN":
			f.Gain, ok = next()
			hasGain = ok
			if ok && i+1 < len(tok) && strings.EqualFold(tok[i+1], "db") {
				i++
			}
		case "Q":
			f.Q, ok = next()
			hasQ = ok
		case "BW":
			if i+1 < len(tok) && strings.EqualFold(tok[i+1], "oct") {
				i++
			}
			bw, ok = next()
			hasBW = ok
		case "T60":
			// Modal's "T60 target 100 ms": not used.
			for i+1 < len(tok) && !strings.EqualFold(tok[i], "ms") {
				i++
			}
			ok = true
		default:
			return Filter{}, fmt.Sprintf("filter parameter %q not understood, ignored", tok[i]), false
		}
		if !ok {
			return Filter{}, fmt.Sprintf("filter parameter %s has no value, ignored", key), false
		}
	}
	if !hasFc {
		return Filter{}, "filter has no Fc, ignored", false
	}
	needsGain := f.Kind == Peak || shelf
	if needsGain && !hasGain {
		return Filter{}, "filter has no Gain, ignored", false
	}
	if !needsGain {
		f.Gain = 0
	}
	msg := ""
	switch {
	case hasQ:
	case hasBW:
		f.Q = qFromBW(bw)
	case slope > 0:
		f.Q = qFromSlope(slope, f.Gain)
	case shelf:
		f.Q = DefaultQ
		msg = "shelf without Q or slope: Q 0.7071 assumed"
	case f.Kind == LowPass || f.Kind == HighPass:
		f.Q = DefaultQ
	default:
		return Filter{}, "peaking filter has no Q, ignored", false
	}
	if err := (Profile{Filters: []Filter{f}}).Validate(); err != nil {
		return Filter{}, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": filter 1: ") + ", ignored", false
	}
	return f, msg, true
}

// qFromBW converts a bandwidth in octaves to Q, as for an analogue filter.
func qFromBW(bw float64) float64 {
	if bw <= 0 {
		return DefaultQ
	}
	p := math.Pow(2, bw)
	return math.Sqrt(p) / (p - 1)
}

// qFromSlope converts a shelf's slope in dB per octave to the cookbook's Q, by
// way of its shelf slope S, where S = 1 is a 12 dB per octave slope.
func qFromSlope(dbPerOct, gain float64) float64 {
	S := dbPerOct / 12
	if S <= 0 {
		return DefaultQ
	}
	A := math.Pow(10, gain/40)
	v := (A+1/A)*(1/S-1) + 2
	if v <= 0 {
		return MaxQ // steeper than the shelf can go at this gain
	}
	return 1 / math.Sqrt(v)
}

// FormatAPO writes p as an Equalizer APO configuration that ParseAPO reads
// back to the same profile. Shelves are written as LSC and HSC with Q.
func FormatAPO(p Profile) string {
	var b strings.Builder
	if p.Name != "" {
		fmt.Fprintf(&b, "# Name: %s\n", strings.ReplaceAll(p.Name, "\n", " "))
	}
	fmt.Fprintf(&b, "Preamp: %s dB\n", num(p.Preamp))
	for i, f := range p.Filters {
		state := "ON"
		if !f.On {
			state = "OFF"
		}
		switch f.Kind {
		case LowPass, HighPass:
			typ := "LPQ"
			if f.Kind == HighPass {
				typ = "HPQ"
			}
			fmt.Fprintf(&b, "Filter %d: %s %s Fc %s Hz Q %s\n", i+1, state, typ, num(f.Freq), num(f.Q))
		default:
			typ := map[Kind]string{Peak: "PK", LowShelf: "LSC", HighShelf: "HSC"}[f.Kind]
			fmt.Fprintf(&b, "Filter %d: %s %s Fc %s Hz Gain %s dB Q %s\n", i+1, state, typ, num(f.Freq), num(f.Gain), num(f.Q))
		}
	}
	return b.String()
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
