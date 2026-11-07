// Package eq is the Equalizer screen, opened from the quick menu: the
// response curve, the preamp, up to peq.MaxFilters bands, presets and
// imports of Equalizer APO / AutoEq files. Every change is heard at once:
// the screen edits the profile the player's processor runs.
//
// Controls follow the handheld's buttons. In the list, Up and Down move and
// A acts. On a band, A opens it for editing: Left and Right pick a field,
// Up and Down change it, L1 and R1 change it in bigger steps, X removes the
// band and A or B closes it.
package eq

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/ujjwal313/boombrick/internal/peq"
	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/screens"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/anim"
	"github.com/ujjwal313/boombrick/internal/ui/gfx"
	"github.com/ujjwal313/boombrick/internal/ui/text"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
)

// Source is the EQ the screen edits. *peq.Manager is one.
type Source interface {
	Profile() peq.Profile
	Bypassed() bool
	Name() string
	SetProfile(p peq.Profile) error
	SetBypass(on bool)
	Stats() peq.Stats
	Presets() []string
	LoadPreset(name string) error
	SavePreset(name string) error
	NewPresetName() string
	DeletePreset(name string) error
	Imports() []string
	Import(path string) (string, []peq.Warning, error)
	Flush() error
}

// rate is the rate the curve is drawn for: the engine's output rate.
const rate = 48000

// The main page's fixed rows; the bands come after them, then the actions.
const (
	rowEnabled = iota
	rowPreamp
	rowAuto
	fixedRows
)

// Actions after the bands.
const (
	actAdd = iota
	actPresets
	actImport
	actReset
	actCount
)

// Fields of a band being edited, left to right.
const (
	fieldOn = iota
	fieldKind
	fieldFreq
	fieldGain
	fieldQ
	fieldCount
)

var fieldNames = [fieldCount]string{"On", "Type", "Frequency", "Gain", "Q"}

// Layout in design pixels.
const (
	side     = 40
	rowInset = 20
	headTop  = widgets.StatusHeight + 8
	graphH   = 132
	rowH     = 54
)

var (
	bandText  = text.Style{Face: text.Figtree, Size: 24, Tabular: true}
	smallText = text.Style{Face: text.Figtree, Size: 20, Tabular: true}
	fieldText = text.Style{Face: text.Figtree, Size: 22, Tabular: true}
	labelText = text.Style{Face: text.Figtree, Size: 26}
)

// page is what the screen shows.
type page uint8

const (
	pageMain page = iota
	pagePresets
	pageImports
)

// Screen is the Equalizer.
type Screen struct {
	src   Source
	toast func(string)

	page   page
	focus  int        // the item focused on the page shown
	k      anim.Value // focus strength on the focused item, for its fade-in
	scroll anim.Value
	edit   bool // the focused band is open for editing
	field  int

	prof     peq.Profile // as last read from src
	curve    []float64   // the response, in dB, at curveFreqs
	peakDB   float64
	peakHz   float64
	clipSeen uint64    // the clip count last looked at
	clipAt   time.Time // when clipping was last seen growing

	list  []string // the presets or imports listed
	empty widgets.Wrapped
}

// New returns the screen.
func New(src Source, toast func(string)) *Screen {
	s := &Screen{src: src, toast: toast}
	s.reload()
	return s
}

// Opened starts on the main page each time the screen opens.
func (s *Screen) Opened() {
	s.page, s.focus, s.edit, s.field = pageMain, 0, false, fieldGain
	s.scroll.Set(0)
	s.k.Set(1)
	s.clipSeen = s.src.Stats().Clipped
	s.clipAt = time.Time{}
	s.reload()
}

// reload reads the profile again and redraws the curve for it.
func (s *Screen) reload() {
	s.prof = s.src.Profile()
	d := s.prof.Design(rate)
	if s.curve == nil {
		s.curve = make([]float64, len(curveFreqs))
	}
	for i, f := range curveFreqs {
		s.curve[i] = d.Response(f)
	}
	s.peakDB, s.peakHz = d.Peak()
}

// curveFreqs are where the curve is evaluated: 20 Hz to 20 kHz, log-spaced.
var curveFreqs = func() []float64 {
	const n = 240
	f := make([]float64, n)
	for i := range f {
		f[i] = 20 * math.Pow(1000, float64(i)/(n-1))
	}
	return f
}()

// set applies p, saving it shortly, and redraws the curve.
func (s *Screen) set(p peq.Profile) bool {
	if err := s.src.SetProfile(p); err != nil {
		s.toast("Can't apply that")
		return false
	}
	s.reload()
	return true
}

// items is how many items the page shows.
func (s *Screen) items() int {
	switch s.page {
	case pagePresets:
		return 1 + len(s.list) // "Save as new" first
	case pageImports:
		return max(len(s.list), 1)
	}
	return fixedRows + len(s.prof.Filters) + actCount
}

// band is the band index focused, or -1.
func (s *Screen) band() int {
	if s.page != pageMain {
		return -1
	}
	if b := s.focus - fixedRows; b >= 0 && b < len(s.prof.Filters) {
		return b
	}
	return -1
}

// action is the action focused, or -1.
func (s *Screen) action() int {
	if s.page != pageMain {
		return -1
	}
	if a := s.focus - fixedRows - len(s.prof.Filters); a >= 0 && a < actCount {
		return a
	}
	return -1
}

// Back closes a band being edited or a sub-page before B closes the screen,
// and writes the EQ out as the screen closes.
func (s *Screen) Back() bool {
	switch {
	case s.edit:
		s.edit = false
		return true
	case s.page != pageMain:
		s.showMain(time.Now())
		return true
	}
	_ = s.src.Flush()
	return false
}

func (s *Screen) showMain(now time.Time) {
	back := fixedRows + len(s.prof.Filters) + actPresets
	if s.page == pageImports {
		back++
	}
	s.page, s.focus = pageMain, back
	s.k.Set(1)
	s.scroll.Set(0)
}

// Handle applies a button.
func (s *Screen) Handle(ev input.Event, now time.Time) bool {
	if ev.Kind == input.Release {
		return false
	}
	if s.edit {
		return s.handleEdit(ev)
	}
	switch ev.Button {
	case input.Up, input.Down:
		step := 1
		if ev.Button == input.Up {
			step = -1
		}
		next := s.focus + step
		if next < 0 || next >= s.items() {
			return false
		}
		s.focus = next
		s.k.Set(0)
		s.k.To(1, now, anim.Focus, anim.EaseOutCubic)
		return true
	case input.Left, input.Right, input.L1, input.R1:
		if s.page == pageMain && s.focus == rowPreamp {
			step := 0.5
			if ev.Button == input.L1 || ev.Button == input.R1 {
				step = 3
			}
			if ev.Button == input.Left || ev.Button == input.L1 {
				step = -step
			}
			p := s.prof.Clone()
			p.Preamp = clamp(math.Round((p.Preamp+step)*10)/10, -peq.MaxPreamp, peq.MaxPreamp)
			return s.set(p)
		}
		return false
	case input.X:
		if s.page == pagePresets && s.focus > 0 {
			name := s.list[s.focus-1]
			if err := s.src.DeletePreset(name); err != nil {
				s.toast("Can't delete " + name)
			} else {
				s.toast("Deleted " + name)
			}
			s.list = s.src.Presets()
			s.focus = min(s.focus, s.items()-1)
			return true
		}
		if b := s.band(); b >= 0 {
			return s.removeBand(b)
		}
		return false
	}
	if ev.Kind != input.Press || ev.Button != input.A {
		return false
	}
	return s.act(now)
}

func (s *Screen) act(now time.Time) bool {
	switch s.page {
	case pagePresets:
		if s.focus == 0 {
			name := s.src.NewPresetName()
			if err := s.src.SavePreset(name); err != nil {
				s.toast("Can't save the preset")
				return true
			}
			s.toast("Saved as " + name)
			s.list = s.src.Presets()
			return true
		}
		name := s.list[s.focus-1]
		if err := s.src.LoadPreset(name); err != nil {
			s.toast("Can't load " + name)
			return true
		}
		s.reload()
		s.toast("Playing with " + name)
		s.showMain(now)
		return true
	case pageImports:
		if len(s.list) == 0 {
			return false
		}
		name, warns, err := s.src.Import(s.list[s.focus])
		if err != nil {
			s.toast("Can't import: " + shortErr(err))
			return true
		}
		s.reload()
		msg := "Imported " + name
		if len(warns) > 0 {
			msg += fmt.Sprintf(" · %d line(s) skipped", len(warns))
		}
		s.toast(msg)
		s.showMain(now)
		return true
	}
	switch s.focus {
	case rowEnabled:
		s.src.SetBypass(!s.src.Bypassed())
		return true
	case rowPreamp:
		return false
	case rowAuto:
		if s.peakDB <= 0.05 {
			s.toast("No boost above 0 dB: nothing to lower")
			return true
		}
		p := s.prof.Clone()
		p.Preamp = math.Floor((p.Preamp-s.peakDB)*10) / 10
		if s.set(p) {
			s.toast(fmt.Sprintf("Preamp set to %s", db(p.Preamp)))
		}
		return true
	}
	if b := s.band(); b >= 0 {
		s.edit = true
		return true
	}
	switch s.action() {
	case actAdd:
		if len(s.prof.Filters) >= peq.MaxFilters {
			s.toast(fmt.Sprintf("At most %d bands", peq.MaxFilters))
			return true
		}
		p := s.prof.Clone()
		p.Filters = append(p.Filters, peq.Filter{On: true, Kind: peq.Peak, Freq: 1000, Q: 1.41})
		if s.set(p) {
			s.focus = fixedRows + len(p.Filters) - 1
			s.edit, s.field = true, fieldFreq
		}
		return true
	case actPresets:
		s.page, s.focus, s.list = pagePresets, 0, s.src.Presets()
		s.scroll.Set(0)
		return true
	case actImport:
		s.page, s.focus, s.list = pageImports, 0, s.src.Imports()
		s.scroll.Set(0)
		return true
	case actReset:
		if s.set(peq.Default()) {
			s.toast("EQ reset to flat")
		}
		return true
	}
	return false
}

func (s *Screen) removeBand(b int) bool {
	p := s.prof.Clone()
	p.Filters = append(p.Filters[:b], p.Filters[b+1:]...)
	if !s.set(p) {
		return false
	}
	s.edit = false
	s.focus = min(s.focus, s.items()-1)
	s.toast(fmt.Sprintf("Band %d removed", b+1))
	return true
}

// handleEdit is a button while a band is open.
func (s *Screen) handleEdit(ev input.Event) bool {
	b := s.band()
	if b < 0 {
		s.edit = false
		return true
	}
	switch ev.Button {
	case input.Left:
		if s.field > 0 {
			s.field--
			return true
		}
		return false
	case input.Right:
		if s.field < fieldCount-1 {
			s.field++
			return true
		}
		return false
	case input.Up, input.Down, input.L1, input.R1:
		dir := 1.0
		if ev.Button == input.Down || ev.Button == input.L1 {
			dir = -1
		}
		coarse := ev.Button == input.L1 || ev.Button == input.R1
		p := s.prof.Clone()
		p.Filters[b] = adjust(p.Filters[b], s.field, dir, coarse)
		return s.set(p)
	case input.X:
		if ev.Kind == input.Press {
			return s.removeBand(b)
		}
	case input.A:
		if ev.Kind == input.Press {
			s.edit = false
			return true
		}
	}
	return false
}

// adjust steps field of f up (dir 1) or down (-1): frequency by a 24th of
// an octave (a third coarse), gain by 0.1 dB (1 dB), Q by about 5 % (25 %).
func adjust(f peq.Filter, field int, dir float64, coarse bool) peq.Filter {
	switch field {
	case fieldOn:
		f.On = !f.On
	case fieldKind:
		k := (int(f.Kind) + int(dir) + 5) % 5
		f.Kind = peq.Kind(k)
	case fieldFreq:
		step := math.Pow(2, 1.0/24)
		if coarse {
			step = math.Pow(2, 1.0/3)
		}
		f.Freq = clamp(roundSig(f.Freq*math.Pow(step, dir), 3), peq.MinFreq, 20000)
	case fieldGain:
		step := 0.1
		if coarse {
			step = 1
		}
		f.Gain = clamp(math.Round((f.Gain+dir*step)*10)/10, -peq.MaxGain, peq.MaxGain)
	case fieldQ:
		step := 1.05
		if coarse {
			step = 1.25
		}
		f.Q = clamp(roundSig(f.Q*math.Pow(step, dir), 3), 0.1, 20)
	}
	return f
}

// Claims keeps L1 and R1 while editing and on the preamp, where they change
// values in big steps.
func (s *Screen) Claims(b input.Button) bool {
	return (b == input.L1 || b == input.R1) && (s.edit || s.page == pageMain && s.focus == rowPreamp)
}

// Animating reports whether focus or the scroll is moving.
func (s *Screen) Animating(now time.Time) bool { return !s.k.Done(now) || !s.scroll.Done(now) }

// Tick asks for a frame a second while music plays, for the clipping
// warning.
func (s *Screen) Tick() time.Duration { return time.Second }

// Draw renders the screen; its focus is gold, as Settings' is.
func (s *Screen) Draw(u *ui.Context) {
	acc := u.Accent
	u.Accent = theme.Fallback()
	defer func() { u.Accent = acc }()
	widgets.DrawBackground(u, widgets.FlatGround)
	widgets.StatusBar(u)
	if st := s.src.Stats(); st.Clipped > s.clipSeen {
		s.clipSeen, s.clipAt = st.Clipped, u.Now
	}

	title := map[page]string{pageMain: "Equalizer", pagePresets: "Presets", pageImports: "Import a preset"}[s.page]
	u.Text(title, text.Title, side, headTop, theme.Paper)
	// The state on the right of the title: what plays, or that it's off.
	state, col := s.src.Name(), theme.Gold
	if s.src.Bypassed() {
		state, col = "Off", theme.Ink3
	}
	sw := u.TextWidth(state, text.Meta)
	u.Text(state, text.Meta, theme.ScreenW-side-sw, u.Baseline(text.Meta, headTop+u.Ascent(text.Title)), col)

	top := headTop + u.LineHeight(text.Title) + 6
	switch s.page {
	case pageMain:
		s.drawGraph(u, gfx.Rect{X: side, Y: top, W: theme.ScreenW - 2*side, H: graphH})
		top += graphH + 6
		s.drawWarning(u, top)
		top += u.LineHeight(smallText) + 8
		s.drawMain(u, top)
	default:
		s.drawList(u, top)
	}
	widgets.HintBar(u, s.hints())
}

// drawWarning says whether the EQ can clip: from the curve, and from what
// was heard.
func (s *Screen) drawWarning(u *ui.Context, y float32) {
	var msg string
	col := theme.Ink3
	switch {
	case !s.clipAt.IsZero() && u.Now.Sub(s.clipAt) < 3*time.Second && !s.src.Bypassed():
		msg, col = "Clipping heard: lower the preamp, or choose Auto preamp", theme.Danger
	case s.peakDB > 0.05:
		msg, col = fmt.Sprintf("Peaks at %s near %s: loud music may clip", db(s.peakDB), hz(s.peakHz)), theme.GoldSoft
	default:
		msg = fmt.Sprintf("Peak %s · no clipping from the EQ", db(s.peakDB))
	}
	u.Text(u.Fit(msg, smallText, theme.ScreenW-2*side), smallText, side, y, col)
}

// drawGraph draws the response curve in box, with its grid and the bands.
func (s *Screen) drawGraph(u *ui.Context, box gfx.Rect) {
	u.Fill(box, theme.PaperA(0.04))
	span := 12.0
	for _, v := range s.curve {
		span = max(span, math.Ceil(math.Abs(v)/6)*6)
	}
	yOf := func(dB float64) float32 { return box.Y + box.H/2 - float32(dB/span)*(box.H/2-6) }
	xOf := func(f float64) float32 { return box.X + float32(math.Log(f/20)/math.Log(1000))*box.W }
	for dB := -span; dB <= span; dB += 6 {
		a := 0.07
		if dB == 0 {
			a = 0.22
		}
		u.Fill(gfx.Rect{X: box.X, Y: yOf(dB), W: box.W, H: 1}, theme.PaperA(a))
	}
	for _, f := range []float64{100, 1000, 10000} {
		u.Fill(gfx.Rect{X: xOf(f), Y: box.Y, W: 1, H: box.H}, theme.PaperA(0.07))
	}
	for _, l := range []struct {
		f float64
		s string
	}{{20, "20"}, {100, "100"}, {1000, "1k"}, {10000, "10k"}} {
		u.Text(l.s, smallText, xOf(l.f)+4, box.Y+box.H-u.LineHeight(smallText), theme.Ink4)
	}
	u.Text(fmt.Sprintf("±%g dB", span), smallText, box.X+box.W-u.TextWidth(fmt.Sprintf("±%g dB", span), smallText)-4, box.Y+2, theme.Ink4)

	line, fill := theme.Gold, theme.Gold
	if s.src.Bypassed() {
		line, fill = theme.PaperA(0.35), theme.PaperA(0.35)
	}
	fill.A = 40
	zero := yOf(0)
	// The curve as columns two pixels wide: the area to 0 dB faintly, and
	// the line itself joining each column to the last.
	prev := yOf(s.curve[0])
	const colW = 2
	for x := float32(0); x < box.W; x += colW {
		f := 20 * math.Pow(1000, float64(x/box.W))
		y := yOf(interp(s.curve, f))
		if y < zero {
			u.Fill(gfx.Rect{X: box.X + x, Y: y, W: colW, H: zero - y}, fill)
		} else {
			u.Fill(gfx.Rect{X: box.X + x, Y: zero, W: colW, H: y - zero}, fill)
		}
		lo, hi := min(prev, y), max(prev, y)
		u.Fill(gfx.Rect{X: box.X + x, Y: lo - 1, W: colW, H: hi - lo + 2.5}, line)
		prev = y
	}
	// The bands: a dot on the curve, the focused one larger, with a line down.
	focus := s.band()
	for i, f := range s.prof.Filters {
		if !f.On || f.Freq < 20 || f.Freq > 20000 {
			continue
		}
		x, y := xOf(f.Freq), yOf(interp(s.curve, f.Freq))
		r := float32(5)
		col := theme.Paper
		if i == focus {
			r, col = 8, theme.GoldSoft
			u.Fill(gfx.Rect{X: x, Y: box.Y, W: 1, H: box.H}, theme.PaperA(0.3))
		}
		widgets.Radio(u, x-r, y-r, 2*r, col, true)
	}
}

// interp is the curve at f, interpolated between the points it was worked out at.
func interp(curve []float64, f float64) float64 {
	pos := math.Log(f/20) / math.Log(1000) * float64(len(curve)-1)
	i := int(math.Floor(pos))
	if i < 0 {
		return curve[0]
	}
	if i >= len(curve)-1 {
		return curve[len(curve)-1]
	}
	t := pos - float64(i)
	return curve[i]*(1-t) + curve[i+1]*t
}

// drawMain draws the main page's list from y down.
func (s *Screen) drawMain(u *ui.Context, top float32) {
	view := gfx.Rect{X: 0, Y: top, W: theme.ScreenW, H: screens.ContentBot - top}
	at := top - float32(s.scroll.At(u.Now))
	u.PushClip(view)
	k := float32(s.k.At(u.Now))
	var focusTop float32
	y := float32(0)
	row := func(i int, draw func(box gfx.Rect, k float32)) {
		box := gfx.Rect{X: side - rowInset, Y: at + y, W: theme.ScreenW - 2*(side-rowInset), H: rowH}
		kk := float32(0)
		if i == s.focus {
			kk, focusTop = k, y
		}
		draw(box, kk)
		y += rowH
	}
	row(rowEnabled, func(b gfx.Rect, k float32) {
		v := "On"
		if s.src.Bypassed() {
			v = "Off (bypassed)"
		}
		s.setting(u, b, k, "sliders", "Equalizer", v, !s.src.Bypassed())
	})
	row(rowPreamp, func(b gfx.Rect, k float32) {
		s.setting(u, b, k, "volume", "Preamp", db(s.prof.Preamp), false)
	})
	row(rowAuto, func(b gfx.Rect, k float32) {
		v := "Safe"
		if s.peakDB > 0.05 {
			v = "Lower by " + db(s.peakDB)[1:]
		}
		s.setting(u, b, k, "check", "Auto preamp", v, s.peakDB > 0.05)
	})
	for i := range s.prof.Filters {
		row(fixedRows+i, func(b gfx.Rect, k float32) { s.drawBand(u, b, k, i) })
	}
	labels := [actCount]struct{ icon, label, value string }{
		{"down", "Add a band", fmt.Sprintf("%d of %d", len(s.prof.Filters), peq.MaxFilters)},
		{"folder", "Presets", ""},
		{"sd", "Import from the card", ""},
		{"refresh", "Reset to flat", ""},
	}
	for a := range actCount {
		l := labels[a]
		row(fixedRows+len(s.prof.Filters)+a, func(b gfx.Rect, k float32) {
			widgets.DrawSetting(u, widgets.Setting{Icon: l.icon, Label: l.label, Value: l.value, Chevron: a == actPresets || a == actImport},
				gfx.Rect{X: b.X, Y: b.Y, W: b.W, H: b.H}, k, a == actCount-1)
		})
	}
	u.PopClip()
	s.follow(focusTop, focusTop+rowH, view.H, y, u.Now)
}

// setting draws a compact Settings row.
func (s *Screen) setting(u *ui.Context, box gfx.Rect, k float32, icon, label, value string, active bool) {
	widgets.DrawSetting(u, widgets.Setting{Icon: icon, Label: label, Value: value, Active: active}, box, k, false)
}

// drawBand draws band i in box: its number, type and settings, or while it
// is edited its fields, the chosen one boxed.
func (s *Screen) drawBand(u *ui.Context, box gfx.Rect, k float32, i int) {
	f := s.prof.Filters[i]
	widgets.SettingFocus(u, box, k)
	u.Fill(gfx.Rect{X: box.X + 16, Y: box.Y + box.H - 1, W: box.W - 32, H: 1}, theme.PaperA(0.14))
	ink := theme.Paper
	if !f.On {
		ink = theme.Ink4
	}
	x := box.X + rowInset
	num := fmt.Sprintf("%d", i+1)
	u.Text(num, bandText, x+(26-u.TextWidth(num, bandText))/2, u.CenterY(bandText, box.Y, box.H), theme.Ink3)
	x += 26 + 18
	vals := [fieldCount]string{onOff(f.On), f.Kind.String(), hz(f.Freq), db(f.Gain), "Q " + qText(f.Q)}
	if f.Kind == peq.LowPass || f.Kind == peq.HighPass {
		vals[fieldGain] = "—"
	}
	if !(s.edit && i == s.band()) {
		cols := [...]float32{0, 130, 260, 400, 540}
		for j := fieldKind; j < fieldCount; j++ {
			u.Text(vals[j], bandText, x+cols[j-1], u.CenterY(bandText, box.Y, box.H), ink)
		}
		if !f.On {
			u.Text("off", bandText, box.X+box.W-rowInset-u.TextWidth("off", bandText), u.CenterY(bandText, box.Y, box.H), theme.Ink4)
		}
		return
	}
	// Editing: each field a cell, the chosen one in a gold box with ▲▼.
	cellW := (box.X + box.W - rowInset - x) / fieldCount
	for j := range fieldCount {
		cell := gfx.Rect{X: x + float32(j)*cellW, Y: box.Y + 6, W: cellW - 8, H: box.H - 12}
		c := theme.Ink2
		if j == s.field {
			widgets.SettingFocus(u, cell, 1)
			c = theme.Paper
		}
		v := vals[j]
		u.Text(u.Fit(v, fieldText, cell.W-12), fieldText, cell.X+(cell.W-u.TextWidth(u.Fit(v, fieldText, cell.W-12), fieldText))/2,
			u.CenterY(fieldText, cell.Y, cell.H), c)
	}
}

// drawList draws the presets or the imports from top down.
func (s *Screen) drawList(u *ui.Context, top float32) {
	view := gfx.Rect{X: 0, Y: top, W: theme.ScreenW, H: screens.ContentBot - top}
	at := top - float32(s.scroll.At(u.Now))
	u.PushClip(view)
	k := float32(s.k.At(u.Now))
	var rows []widgets.Setting
	if s.page == pagePresets {
		rows = append(rows, widgets.Setting{Icon: "download", Label: "Save as a new preset", Value: s.src.NewPresetName()})
		for _, n := range s.list {
			r := widgets.Setting{Icon: "sliders", Label: n}
			if n == s.src.Name() {
				r.Value, r.Active = "Playing", true
			}
			rows = append(rows, r)
		}
	} else {
		for _, p := range s.list {
			rows = append(rows, widgets.Setting{Icon: "sd", Label: strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), Value: filepath.Base(filepath.Dir(p))})
		}
		if len(rows) == 0 {
			u.PopClip()
			u.Text("No presets to import", text.Heading, side, top+24, theme.Paper)
			y := top + 24 + u.LineHeight(text.Heading) + 8
			for _, l := range s.empty.Lines(u, "Put Equalizer APO or AutoEq ParametricEQ.txt files in the EQ folder of your music, then open this again.", text.Body, theme.ScreenW-2*side) {
				u.Text(l, text.Body, side, y, theme.Ink3)
				y += u.LineHeight(text.Body)
			}
			return
		}
	}
	var focusTop float32
	for i, r := range rows {
		y := float32(i) * widgets.SettingHeight
		kk := float32(0)
		if i == s.focus {
			kk, focusTop = k, y
		}
		widgets.DrawSetting(u, r, gfx.Rect{X: side - rowInset, Y: at + y, W: theme.ScreenW - 2*(side-rowInset), H: widgets.SettingHeight}, kk, i == len(rows)-1)
	}
	u.PopClip()
	s.follow(focusTop, focusTop+widgets.SettingHeight, view.H, float32(len(rows))*widgets.SettingHeight, u.Now)
}

// follow scrolls so the focused item, top to bot down the list, is in view.
func (s *Screen) follow(top, bot, view, total float32, now time.Time) {
	to := max(s.scroll.Target(), float64(bot)-float64(view))
	to = min(to, float64(top))
	to = max(0, min(to, float64(total-view)))
	if to != s.scroll.Target() {
		s.scroll.To(to, now, anim.Focus, anim.EaseOutCubic)
	}
}

// hints are the hint bar's for what is focused.
func (s *Screen) hints() []widgets.Hint {
	switch {
	case s.edit:
		return []widgets.Hint{{Key: "◀ ▶", Label: fieldNames[s.field]}, {Key: "▲ ▼", Label: "Adjust"},
			{Key: "L1/R1", Label: "Big steps"}, {Key: "X", Label: "Remove"}, {Key: "A", Label: "Done"}}
	case s.page == pagePresets && s.focus == 0:
		return []widgets.Hint{{Key: "A", Label: "Save"}, {Key: "B", Label: "Back"}}
	case s.page == pagePresets:
		return []widgets.Hint{{Key: "A", Label: "Load"}, {Key: "X", Label: "Delete"}, {Key: "B", Label: "Back"}}
	case s.page == pageImports:
		return []widgets.Hint{{Key: "A", Label: "Import"}, {Key: "B", Label: "Back"}}
	case s.focus == rowPreamp:
		return []widgets.Hint{{Key: "◀ ▶", Label: "0.5 dB"}, {Key: "L1/R1", Label: "3 dB"}, {Key: "B", Label: "Back"}}
	case s.band() >= 0:
		return []widgets.Hint{{Key: "A", Label: "Edit band"}, {Key: "X", Label: "Remove"}, {Key: "B", Label: "Back"}}
	case s.focus == rowEnabled:
		return []widgets.Hint{{Key: "A", Label: "Switch"}, {Key: "B", Label: "Back"}}
	}
	return []widgets.Hint{{Key: "A", Label: "Open"}, {Key: "B", Label: "Back"}}
}

func onOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// db formats a level with its sign: "+6.4 dB", "−3 dB", "0 dB".
func db(v float64) string {
	v = math.Round(v*10) / 10
	switch {
	case v > 0:
		return fmt.Sprintf("+%s dB", trim(v))
	case v < 0:
		return fmt.Sprintf("−%s dB", trim(-v))
	}
	return "0 dB"
}

// hz formats a frequency: "105 Hz", "8.8 kHz".
func hz(f float64) string {
	if f >= 1000 {
		return trim(roundSig(f/1000, 3)) + " kHz"
	}
	return trim(math.Round(f)) + " Hz"
}

func qText(q float64) string { return fmt.Sprintf("%.2f", q) }

func trim(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func roundSig(v float64, digits int) float64 {
	if v == 0 {
		return 0
	}
	p := math.Pow(10, float64(digits)-math.Ceil(math.Log10(math.Abs(v))))
	return math.Round(v*p) / p
}

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 {
		s = s[i+2:]
	}
	return s
}
