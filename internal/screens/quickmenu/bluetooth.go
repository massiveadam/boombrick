package quickmenu

import (
	"time"

	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/anim"
	"github.com/ujjwal313/boombrick/internal/ui/gfx"
	"github.com/ujjwal313/boombrick/internal/ui/text"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
)

// BTDevice is a paired headset or speaker.
type BTDevice struct {
	Addr, Name string
	Connected  bool
}

// BTList is what the Bluetooth page shows.
type BTList struct {
	Devices []BTDevice
	Loaded  bool   // read at least once
	Off     bool   // no Bluetooth to ask: it is off
	Busy    string // the address being connected or disconnected
	Failed  string // the address whose last connect or disconnect failed
}

// connected is the name of the device connected, or "".
func (l BTList) connected() string {
	for _, d := range l.Devices {
		if d.Connected {
			return d.Name
		}
	}
	return ""
}

// summary is the Bluetooth row's value.
func (l BTList) summary() string {
	switch {
	case !l.Loaded:
		return ""
	case l.Off:
		return "Off"
	case l.connected() != "":
		return l.connected()
	}
	return "Not connected"
}

// status is a device's value on the page.
func (l BTList) status(d BTDevice) string {
	switch {
	case l.Busy == d.Addr && d.Connected:
		return "Disconnecting…"
	case l.Busy == d.Addr:
		return "Connecting…"
	case l.Failed == d.Addr && !d.Connected:
		return "Didn't connect"
	case d.Connected:
		return "Connected"
	}
	return "Not connected"
}

var noteText = text.Style{Face: text.Figtree, Size: 26}

// btEvery is how often the open page reads the devices again, to follow a
// headset another device took, or one out of its case.
const btEvery = 3 * time.Second

// btPage is the quick menu's Bluetooth page: the paired headsets and
// speakers, A to connect or disconnect one.
type btPage struct {
	open  bool
	focus int
	k     anim.Value // the focus ring's move
	asked time.Time  // when the list was last asked for
}

func (m *Menu) openBT(now time.Time) {
	m.bt = btPage{open: true, asked: now}
	m.bt.k.Set(1)
	m.act.RefreshBT()
}

// handleBT moves between the devices, connects or disconnects one with A,
// goes back to the menu with B and closes it with Menu.
func (m *Menu) handleBT(ev input.Event, now time.Time) bool {
	n := len(m.act.Bluetooth().Devices)
	switch ev.Button {
	case input.Up, input.Down:
		to := m.bt.focus + 1
		if ev.Button == input.Up {
			to = m.bt.focus - 1
		}
		if to < 0 || to >= n {
			return false
		}
		m.bt.focus = to
		m.bt.k.Set(0)
		m.bt.k.To(1, now, anim.Focus, anim.EaseOutCubic)
		return true
	}
	if ev.Kind != input.Press {
		return false
	}
	switch ev.Button {
	case input.A:
		l := m.act.Bluetooth()
		if m.bt.focus >= len(l.Devices) || l.Busy != "" {
			return false
		}
		m.act.ToggleBT(l.Devices[m.bt.focus].Addr)
	case input.B:
		m.bt.open = false
	case input.Menu:
		m.close(now)
	default:
		return false
	}
	return true
}

// drawBT draws the Bluetooth page in the panel, and its hints.
func (m *Menu) drawBT(u *ui.Context, shown float32) {
	if m.open && !u.Now.Before(m.bt.asked.Add(btEvery)) {
		m.bt.asked = u.Now
		m.act.RefreshBT()
	}
	l := m.act.Bluetooth()
	rows := make([]widgets.Setting, 0, len(l.Devices))
	for _, d := range l.Devices {
		icon := "headphones"
		if !d.Connected {
			icon = "bluetooth"
		}
		rows = append(rows, widgets.Setting{Icon: icon, Label: d.Name, Value: l.status(d), Active: d.Connected})
	}
	if m.bt.focus >= len(rows) {
		m.bt.focus = max(len(rows)-1, 0)
	}
	note := ""
	switch {
	case !l.Loaded:
		note = "Looking…"
	case l.Off:
		note = "Bluetooth is off"
	case len(rows) == 0:
		note = "No headphones paired yet"
	}
	head := padTop + u.LineHeight(title) + 20
	body := float32(len(rows) * widgets.SettingHeight)
	if note != "" {
		body = widgets.SettingHeight
	}
	maxH := float32(theme.ScreenH - 2*(widgets.HintsHeight+8))
	view := min(body, maxH-head-padTop)
	p := widgets.BeginPanel(u, head+view+padTop, shown)
	box := p.Box
	y := box.Y + padTop
	u.Text("Bluetooth", title, box.X+40, y, theme.Paper)
	y += u.LineHeight(title) + 20
	u.PushClip(gfx.Rect{X: box.X, Y: y, W: box.W, H: view})
	if note != "" {
		u.Text(note, noteText, box.X+40, u.CenterY(noteText, y, widgets.SettingHeight), theme.Ink2)
	}
	if over := float32((m.bt.focus+1)*widgets.SettingHeight) - view; over > 0 {
		y -= over // the focused one in view
	}
	for i, s := range rows {
		k := float32(0)
		if i == m.bt.focus {
			k = float32(m.bt.k.At(u.Now))
		}
		row := gfx.Rect{X: box.X + padSide, Y: y, W: box.W - 2*padSide, H: widgets.SettingHeight}
		widgets.DrawSetting(u, s, row, k, i == len(rows)-1)
		y += widgets.SettingHeight
	}
	u.PopClip()
	p.End(u)
	if m.open {
		hints := []widgets.Hint{{Key: "B", Label: "Back"}}
		if m.bt.focus < len(l.Devices) && l.Busy == "" {
			act := "Connect"
			if l.Devices[m.bt.focus].Connected {
				act = "Disconnect"
			}
			hints = append([]widgets.Hint{{Key: "A", Label: act}}, hints...)
		}
		u.Fill(gfx.Rect{Y: theme.ScreenH - widgets.HintsHeight, W: theme.ScreenW, H: widgets.HintsHeight}, theme.Ground)
		widgets.HintBar(u, hints)
	}
}
