package settings

import (
	"time"

	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/screens/home"
	keys "github.com/ujjwal313/boombrick/internal/settings"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/anim"
	"github.com/ujjwal313/boombrick/internal/ui/gfx"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
)

// homePage is the Home page: Home's blocks in order, each on or off. A
// switches the focused one, X and Y move it up and down; each change is kept
// at once and Home follows it.
type homePage struct {
	blocks []home.Block // every block: those shown first, then any this build can't show
	shown  int          // how many are shown
	focus  int
	k      anim.Value // the focus ring's move
	hints  []widgets.Hint
}

func newHomePage(s *Screen) *homePage {
	p := &homePage{hints: []widgets.Hint{{Key: "A", Label: "Switch"}, {Key: "X", Label: "Up"}, {Key: "Y", Label: "Down"}, {Key: "B", Label: "Back"}}}
	var hidden []home.Block
	for _, b := range home.Blocks(s.opts.Prefs.String(keys.HomeBlocks)) {
		if b.ID == home.BlockSpotify && s.opts.Spotify == nil {
			hidden = append(hidden, b) // kept as it is, for a build with Spotify
			continue
		}
		p.blocks = append(p.blocks, b)
	}
	p.shown = len(p.blocks)
	p.blocks = append(p.blocks, hidden...)
	return p
}

func (p *homePage) handle(s *Screen, ev input.Event, now time.Time) bool {
	if ev.Kind == input.Release {
		return false
	}
	move := func(to int) bool {
		if to < 0 || to >= p.shown {
			return false
		}
		p.focus = to
		p.k.Set(0)
		p.k.To(1, now, anim.Focus, anim.EaseOutCubic)
		return true
	}
	switch ev.Button {
	case input.Up:
		return move(p.focus - 1)
	case input.Down:
		return move(p.focus + 1)
	}
	if ev.Kind != input.Press {
		return false
	}
	switch ev.Button {
	case input.A:
		p.blocks[p.focus].On = !p.blocks[p.focus].On
	case input.X, input.Y:
		to := p.focus - 1
		if ev.Button == input.Y {
			to = p.focus + 1
		}
		if to < 0 || to >= p.shown {
			return false
		}
		p.blocks[p.focus], p.blocks[to] = p.blocks[to], p.blocks[p.focus]
		p.focus = to
	default:
		return false
	}
	s.opts.Prefs.Set(keys.HomeBlocks, home.BlocksSetting(p.blocks))
	if s.opts.HomeChanged != nil {
		s.opts.HomeChanged()
	}
	return true
}

func (p *homePage) back(s *Screen) bool {
	s.homePage = nil
	return true
}

func (p *homePage) animating(now time.Time) bool { return !p.k.Done(now) }

func (p *homePage) draw(s *Screen, u *ui.Context) {
	widgets.StatusBar(u)
	header(u, "Home")
	y := listTop(u)
	for i, b := range p.blocks[:p.shown] {
		row := gfx.Rect{X: side - rowInset, Y: y, W: theme.ScreenW - 2*(side-rowInset), H: cacheRowH}
		if i == p.focus {
			widgets.SettingFocus(u, row, 1)
		}
		ink, value := theme.Paper, "On"
		if !b.On {
			ink, value = theme.Ink3, "Off"
		}
		u.Text(b.Label, labelText, side, u.CenterY(labelText, y, cacheRowH), ink)
		w := u.TextWidth(value, valueText)
		vcol := theme.Ink2
		if b.On {
			vcol = theme.Gold
		}
		u.Text(value, valueText, theme.ScreenW-side-w, u.CenterY(valueText, y, cacheRowH), vcol)
		y += cacheRowH
	}
	widgets.HintBar(u, p.hints)
}
