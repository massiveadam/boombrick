package browse

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/screens"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/gfx"
	"github.com/ujjwal313/boombrick/internal/ui/text"
	"github.com/ujjwal313/boombrick/internal/ui/views"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
)

// The search page: the keyboard on the left, the results on the right under
// their label, found as each letter is typed.
const (
	kbX       = side
	kbY       = contentTop + pad
	resultsX  = kbX + widgets.KeyboardW + 28
	resultsW  = theme.ScreenW - side - resultsX
	resultTop = kbY + 30 // under the label
	resultH   = 76
	maxQuery  = 60 // runes
)

var labelText = text.Style{Face: text.Figtree, Size: 20}

// result is one thing found: an artist, an album or a song.
type result struct {
	artist *library.Artist
	album  *library.Album
	song   *library.Track
	row    widgets.Row
	art    widgets.ArtSpec
}

// Search is the Library's search: artists, albums and songs, by name.
type Search struct {
	nav       screens.Nav
	p         Player
	ix        *library.Index
	kb        widgets.Keyboard
	query     string
	found     string // the query the results are for
	results   []result
	list      views.Grid
	onResults bool
	msg       widgets.Wrapped
}

// NewSearch opens the search of ix, with focus on the keyboard.
func NewSearch(nav screens.Nav, p Player, ix *library.Index) *Search {
	s := &Search{nav: nav, p: p, ix: ix, kb: widgets.NewKeyboard()}
	s.list = views.Grid{Cols: 1, CellW: resultsW, CellH: resultH, Pad: pad, ViewH: contentBot - resultTop}
	s.list.SetActive(false, time.Time{})
	return s
}

// Handle types on the keyboard and moves to the results with Y, ▶ or
// Search; on the results A opens or plays, Y queues and B goes back to the
// keyboard. On the keyboard B deletes, and goes back once nothing is typed.
func (s *Search) Handle(ev input.Event, now time.Time) bool {
	if ev.Kind == input.Release {
		return false
	}
	if s.onResults {
		return s.handleResults(ev, now)
	}
	if ev.Kind == input.Press {
		switch ev.Button {
		case input.A:
			switch act, r := s.kb.Press(); act {
			case widgets.TypeKey:
				s.typeRune(r)
			case widgets.SpaceKey:
				s.space()
			case widgets.BackspaceKey:
				s.backspace()
			case widgets.SearchKey:
				s.toResults(now)
			}
			return true
		case input.B:
			return s.backspace()
		case input.X:
			return s.space()
		case input.Y, input.Start:
			return s.toResults(now)
		}
	}
	if s.kb.Move(ev.Button, now) {
		return true
	}
	return ev.Button == input.Right && s.toResults(now)
}

func (s *Search) handleResults(ev input.Event, now time.Time) bool {
	if ev.Kind == input.Press {
		r := &s.results[s.list.Focus()]
		switch ev.Button {
		case input.A:
			s.open(r)
			return true
		case input.Y:
			s.p.Enqueue(r.tracks())
			s.nav.Toast(screens.AddedToQueue)
			return true
		case input.B:
			s.toKeyboard(now)
			return true
		}
	}
	if s.list.Move(ev.Button, now) {
		return true
	}
	if ev.Button == input.Left {
		s.toKeyboard(now)
		return true
	}
	return false
}

// open opens an artist's or album's page, or plays a song's album from it.
func (s *Search) open(r *result) {
	switch {
	case r.artist != nil:
		s.nav.Push(NewArtistPage(s.nav, s.p, r.artist))
	case r.album != nil:
		s.nav.Push(NewAlbumPage(s.nav, s.p, r.album))
	default:
		s.p.SetShuffle(false)
		if al := s.ix.AlbumOf(r.song.Path); al != nil {
			for i := range al.Tracks {
				if al.Tracks[i].Path == r.song.Path {
					s.p.Play(screens.Tracks(al.Tracks), i)
					return
				}
			}
		}
		s.p.Play([]player.Track{screens.TrackOf(r.song)}, 0)
	}
}

func (r *result) tracks() []player.Track {
	switch {
	case r.artist != nil:
		return artistTracks(r.artist)
	case r.album != nil:
		return screens.Tracks(r.album.Tracks)
	}
	return []player.Track{screens.TrackOf(r.song)}
}

// Claims keeps B while it deletes or leaves the results, and Start while
// there is something to search: here they don't go back or open Now Playing.
func (s *Search) Claims(b input.Button) bool {
	switch b {
	case input.B:
		return s.onResults || s.query != ""
	case input.Start:
		return !s.onResults && strings.TrimSpace(s.query) != ""
	}
	return false
}

func (s *Search) typeRune(r rune) {
	if utf8.RuneCountInString(s.query) < maxQuery {
		s.query += string(r)
	}
}

// space adds a space between words: never a leading one or two together.
func (s *Search) space() bool {
	if s.query == "" || strings.HasSuffix(s.query, " ") {
		return false
	}
	s.typeRune(' ')
	return true
}

func (s *Search) backspace() bool {
	if s.query == "" {
		return false
	}
	_, n := utf8.DecodeLastRuneInString(s.query)
	s.query = s.query[:len(s.query)-n]
	return true
}

func (s *Search) toResults(now time.Time) bool {
	s.refresh()
	if len(s.results) == 0 {
		return false
	}
	s.onResults = true
	s.kb.SetActive(false, now)
	s.list.SetActive(true, now)
	return true
}

func (s *Search) toKeyboard(now time.Time) {
	s.onResults = false
	s.list.SetActive(false, now)
	s.kb.SetActive(true, now)
}

// refresh finds the query's results, unless they are found already.
func (s *Search) refresh() {
	q := library.Fold(s.query)
	if q == s.found {
		return
	}
	s.found = q
	f := s.ix.Search(q)
	s.results = s.results[:0]
	for _, ar := range f.Artists {
		r := result{artist: ar, art: screens.ArtistArt(ar)}
		r.row = widgets.Row{Title: ar.Name, Subtitle: "Artist · " + artistLine(len(ar.Albums), ar.Songs), Round: true}
		s.results = append(s.results, r)
	}
	for _, al := range f.Albums {
		r := result{album: al, art: screens.AlbumArt(al)}
		sub := "Album · " + al.Artist
		if al.Year > 0 {
			sub += " · " + strconv.Itoa(al.Year)
		}
		r.row = widgets.Row{Title: al.Title, Subtitle: sub}
		s.results = append(s.results, r)
	}
	for _, t := range f.Songs {
		r := result{song: t, art: screens.SongArt(t)}
		r.row = widgets.Row{Title: t.Title, Subtitle: "Song · " + t.Artist}
		if t.Duration > 0 {
			r.row.Duration = screens.Clock(t.Duration)
		}
		s.results = append(s.results, r)
	}
	for i := range s.results {
		s.results[i].row.Art = &s.results[i].art
	}
	s.list.SetCountFocus(len(s.results), 0)
	if !s.onResults {
		s.list.SetActive(false, time.Time{}) // a new list starts active
	}
}

// Animating reports whether focus is moving.
func (s *Search) Animating(now time.Time) bool { return s.kb.Animating(now) || s.list.Animating(now) }

// Draw renders the frame.
func (s *Search) Draw(u *ui.Context) {
	s.refresh()
	widgets.DrawBackground(u, widgets.TabGround)
	s.kb.Draw(u, kbX, kbY, s.query)
	label := "SEARCH YOUR LIBRARY"
	if len(s.results) > 0 {
		label = screens.Count(len(s.results))
		if len(s.results) == 1 {
			label = "1 RESULT"
		} else {
			label += " RESULTS"
		}
	}
	u.Text(label, labelText, resultsX, kbY, theme.Ink3)
	switch {
	case len(s.results) > 0:
		s.drawResults(u)
	default:
		msg := "Type to find artists, albums and songs."
		if strings.TrimSpace(s.query) != "" {
			msg = "Nothing in your library matches “" + strings.TrimSpace(s.query) + "”"
		}
		message(u, &s.msg, msg, gfx.Rect{X: resultsX, Y: resultTop, W: resultsW, H: contentBot - resultTop})
	}
	widgets.StatusBar(u)
	widgets.Tabs(u, 1)
	widgets.HintBar(u, s.hints())
}

func (s *Search) drawResults(u *ui.Context) {
	playing := s.p.State().Track.Path
	u.PushClip(screens.GlowArea)
	s.list.Draw(u.Now, resultsX, resultTop, func(i int, x, y, k float32) {
		r := s.results[i].row
		r.Playing = s.results[i].song != nil && s.results[i].song.Path == playing
		box := gfx.Rect{X: x, Y: y, W: resultsW, H: resultH}
		widgets.RowGlow(u, box, widgets.RowRadius, k)
		u.PushClip(screens.Below(resultTop))
		widgets.ListRow(u, r, box, k)
		u.PopClip()
	})
	u.PopClip()
}

// message draws m wrapped and centred in box.
func message(u *ui.Context, c *widgets.Wrapped, m string, box gfx.Rect) {
	lines := c.Lines(u, m, text.Body, box.W)
	lh := u.LineHeight(text.Body)
	y := box.Y + (box.H-float32(len(lines))*lh)/2
	for _, l := range lines {
		u.Text(l, text.Body, box.X+(box.W-u.TextWidth(l, text.Body))/2, y, theme.Ink2)
		y += lh
	}
}

var (
	searchTypingHints = []widgets.Hint{{Key: "A", Label: "Type"}, {Key: "B", Label: "Delete"}, {Key: "X", Label: "Space"},
		{Key: "Y", Label: "To results"}}
	searchEmptyHints = []widgets.Hint{{Key: "A", Label: "Type"}, {Key: "B", Label: "Back"}}
	searchOpenHints  = []widgets.Hint{{Key: "A", Label: "Open"}, {Key: "Y", Label: "Add to queue"}, {Key: "B", Label: "Keyboard"}}
	searchPlayHints  = []widgets.Hint{{Key: "A", Label: "Play"}, {Key: "Y", Label: "Add to queue"}, {Key: "B", Label: "Keyboard"}}
)

func (s *Search) hints() []widgets.Hint {
	switch {
	case s.onResults && s.results[s.list.Focus()].song != nil:
		return searchPlayHints
	case s.onResults:
		return searchOpenHints
	case s.query == "":
		return searchEmptyHints
	}
	return searchTypingHints
}
