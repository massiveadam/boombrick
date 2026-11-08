package youtube

import (
	"slices"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/media"
	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/screens"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui/anim"
	"github.com/ujjwal313/boombrick/internal/ui/gfx"
	"github.com/ujjwal313/boombrick/internal/ui/raster"
	"github.com/ujjwal313/boombrick/internal/ui/uitest"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
	"github.com/ujjwal313/boombrick/internal/ytmusic"
)

func only(items []ytmusic.Item, k ytmusic.Kind) []ytmusic.Item {
	return slices.DeleteFunc(slices.Clone(items), func(it ytmusic.Item) bool { return it.Kind != k })
}

var nightStates = fakeDownloads{"v1": {media.Downloading, 0.62}, "s2": {media.Saved, 0}, "s3": {media.Queued, 0},
	"s4": {media.Failed, 0}}

func TestResults(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{
		"night drive All":   nightResults,
		"night drive Songs": only(nightResults, ytmusic.SongItem),
	}}
	nav := &fakeNav{}
	r := NewResults(nav, src, nightStates, "night drive")
	uitest.Golden(t, "yt-results-all", render(t, r, t0))

	if r.Handle(press(input.L2), t0) {
		t.Fatal("L2 on the first filter went somewhere")
	}
	r.Handle(press(input.R2), t0)
	uitest.Golden(t, "yt-results-songs", render(t, r, t0))
	r.Handle(press(input.R2), t0) // Albums, not answered yet
	render(t, r, t0)
	if !slices.Contains(src.searched, "night drive Albums") {
		t.Fatal("the Albums filter wasn't asked for")
	}
	r.Handle(press(input.L2), t0)
	r.Handle(press(input.L2), t0) // back to All, where focus is still on the top result

	r.Handle(press(input.A), t0)
	if len(nav.pushed) != 1 {
		t.Fatalf("%d pages opened", len(nav.pushed))
	}
	if _, ok := nav.pushed[0].(*AlbumPage); !ok {
		t.Fatalf("the album result opened %T", nav.pushed[0])
	}
	r.Handle(press(input.Down), t0)
	if !r.Handle(press(input.A), t0) || len(nav.pushed) != 1 {
		t.Fatal("A on a song should play it, opening nothing")
	}
	r.Handle(press(input.Down), t0)
	r.Handle(press(input.A), t0)
	if _, ok := nav.pushed[1].(*ArtistPage); !ok {
		t.Fatalf("the artist result opened %T", nav.pushed[1])
	}
}

func TestResultActions(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults},
		albums: map[string]ytmusic.AlbumPage{"MPREb_nd": nightDrive}}
	nav, dl := &fakeNav{}, &recorder{}
	r := NewResults(nav, src, dl, "night drive")
	render(t, r, t0)
	r.Handle(press(input.Down), t0) // the song
	for _, b := range []input.Button{input.A, input.X, input.Y} {
		r.Handle(press(b), t0)
	}
	if !slices.Equal(dl.played, []string{"v1@0"}) || len(dl.got) != 1 || !slices.Equal(dl.got[0], []string{"v1"}) ||
		len(dl.queued) != 1 || !slices.Equal(dl.queued[0], []string{"v1"}) {
		t.Fatalf("played %q, got %q, queued %q", dl.played, dl.got, dl.queued)
	}
	if want := []string{"Downloading “Night Drive Theory”", screens.AddedToQueue}; !slices.Equal(nav.toasts, want) {
		t.Fatalf("toasts %q, want %q", nav.toasts, want)
	}
	r.Handle(press(input.Up), t0) // the album
	r.Handle(press(input.X), t0)
	render(t, r, t0) // its page is there: its songs are got
	if len(dl.got) != 2 || !slices.Equal(dl.got[1], []string{"v1", "v2", "v3", "v4", "v5"}) ||
		nav.toasts[len(nav.toasts)-1] != "Downloading 5 songs" {
		t.Fatalf("got %q, toasts %q", dl.got, nav.toasts)
	}
	uitest.Golden(t, "yt-results-album", render(t, r, t0.Add(anim.Focus)))
	if len(dl.got) != 2 {
		t.Fatalf("the album was got again: %q", dl.got)
	}
}

func TestYOnAnAlbumOrArtistDoesNothing(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	nav, dl := &fakeNav{}, &recorder{}
	r := NewResults(nav, src, dl, "night drive")
	render(t, r, t0)
	for _, row := range []int{0, 2} { // the album, the artist
		for r.views[r.filter].Focus() < row {
			r.Handle(press(input.Down), t0)
		}
		if r.Handle(press(input.Y), t0) {
			t.Errorf("Y on %q did something", nightResults[row].Title)
		}
	}
	if len(dl.queued) != 0 || len(dl.got) != 0 || len(nav.toasts) != 0 || len(nav.pushed) != 0 {
		t.Fatalf("queued %q, got %q, toasts %q, pushed %d", dl.queued, dl.got, nav.toasts, len(nav.pushed))
	}
}

// Albums asked for one after another are all got, each once, whichever
// page comes first.
func TestDownloadingTwoAlbums(t *testing.T) {
	classics := ytmusic.AlbumPage{ID: "MPREb_nc", Title: "Night Classics", Artist: "Various Artists", Tracks: []ytmusic.Track{
		{VideoID: "c1", Number: 1, Title: "Moonlight Drive"}, {VideoID: "c2", Number: 2, Title: "Nightcall"}}}
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}, albums: map[string]ytmusic.AlbumPage{}}
	nav, dl := &fakeNav{}, &recorder{}
	r := NewResults(nav, src, dl, "night drive")
	render(t, r, t0)
	r.Handle(press(input.X), t0) // Night Drive Theory
	for r.views[r.filter].Focus() < 6 {
		r.Handle(press(input.Down), t0)
	}
	r.Handle(press(input.X), t0) // Night Classics
	r.Handle(press(input.X), t0) // again
	render(t, r, t0)
	if len(dl.got) != 0 || len(r.albums) != 2 {
		t.Fatalf("got %q, waiting on %q, before the pages came", dl.got, r.albums)
	}
	src.albums["MPREb_nc"] = classics
	render(t, r, t0)
	src.albums["MPREb_nd"] = nightDrive
	render(t, r, t0)
	if len(dl.got) != 2 || !slices.Equal(dl.got[0], []string{"c1", "c2"}) || len(dl.got[1]) != 5 || len(r.albums) != 0 {
		t.Fatalf("got %q, still waiting on %q", dl.got, r.albums)
	}
}

// An album asked for on the results is got once its page comes, though the
// results have closed by then: the typing screen under them carries on.
func TestAnAlbumOutlivesTheResults(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}, albums: map[string]ytmusic.AlbumPage{}}
	nav, dl := &fakeNav{}, &recorder{}
	s := NewSearch(nav, src, dl)
	s.query = "night drive"
	render(t, s, t0)
	s.Handle(press(input.Start), t0)
	r := nav.pushed[0].(*Results)
	render(t, r, t0)
	r.Handle(press(input.X), t0) // the album, whose page hasn't come
	render(t, r, t0)
	nav.Pop() // B
	src.albums["MPREb_nd"] = nightDrive
	render(t, s, t0)
	if len(dl.got) != 1 || len(dl.got[0]) != 5 || !slices.Equal(nav.toasts, []string{"Downloading 5 songs"}) {
		t.Fatalf("got %q, toasts %q", dl.got, nav.toasts)
	}
}

// An album page opened over the results gets what they asked for too.
func TestAnAlbumPageCarriesOnTheResultsDownloads(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}, albums: map[string]ytmusic.AlbumPage{}}
	nav, dl := &fakeNav{}, &recorder{}
	r := NewResults(nav, src, dl, "night drive")
	render(t, r, t0)
	r.Handle(press(input.X), t0) // Night Drive Theory, whose page hasn't come
	r.Handle(press(input.A), t0) // its page opens
	src.albums["MPREb_nd"] = nightDrive
	render(t, nav.pushed[0], t0)
	if len(dl.got) != 1 || len(dl.got[0]) != 5 {
		t.Fatalf("got %q", dl.got)
	}
}

// So does an artist's page, and an album page opened from that.
func TestAnArtistPageCarriesOnTheResultsDownloads(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}, albums: map[string]ytmusic.AlbumPage{}}
	nav, dl := &fakeNav{}, &recorder{}
	r := NewResults(nav, src, dl, "night drive")
	render(t, r, t0)
	r.Handle(press(input.X), t0) // Night Drive Theory, whose page hasn't come
	for r.views[r.filter].Focus() < 2 {
		r.Handle(press(input.Down), t0)
	}
	r.Handle(press(input.A), t0) // the artist's page opens
	pg, ok := nav.pushed[0].(*ArtistPage)
	if !ok {
		t.Fatalf("opened %T, want the artist's page", nav.pushed[0])
	}
	if pg.shared != r.shared {
		t.Fatal("the artist's page doesn't share the results' downloads")
	}
	src.albums["MPREb_nd"] = nightDrive
	render(t, pg, t0)
	if len(dl.got) != 1 || len(dl.got[0]) != 5 {
		t.Fatalf("got %q", dl.got)
	}
}

// An album whose page can't come is given up with a toast.
func TestDownloadingAnAlbumThatCantLoad(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	nav, dl := &fakeNav{}, &recorder{}
	r := NewResults(nav, src, dl, "night drive")
	r.Handle(press(input.X), t0)
	render(t, r, t0) // still waiting
	if len(nav.toasts) != 0 || r.Animating(t0.Add(time.Second)) {
		t.Fatalf("toasts %q while the page loads", nav.toasts)
	}
	src.state = ytmusic.Failed
	u, _ := uitest.NewContext(t, theme.DeviceW, theme.DeviceH)
	at := t0.Add(time.Second)
	u.BeginFrame(at)
	r.Draw(u)
	if len(dl.got) != 0 || !slices.Equal(nav.toasts, []string{"Couldn’t get the album’s songs"}) {
		t.Fatalf("got %q, toasts %q", dl.got, nav.toasts)
	}
}

func TestResultHintsFit(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	for _, dl := range []Downloads{nil, &recorder{}} {
		r := NewResults(&fakeNav{}, src, dl, "night drive")
		render(t, r, t0)
		u, _ := uitest.NewContext(t, 64, 64)
		for i, it := range nightResults {
			if r.views[r.filter].Focus() != i {
				t.Fatalf("focus on %d, want %d", r.views[r.filter].Focus(), i)
			}
			h := r.hints()
			if w := widgets.HintsWidth(u, h); w > widgets.MaxHintsWidth {
				t.Errorf("kind %d: hints %v are %v wide", it.Kind, h, w)
			}
			if theme.Brick && dl != nil && it.Kind == ytmusic.SongItem && !slices.Equal(keys(h), []string{"A", "X", "Y", "L2/R2"}) {
				t.Errorf("a song's hints are %v", h)
			}
			r.Handle(press(input.Down), t0)
		}
	}
}

func keys(h []widgets.Hint) []string {
	var out []string
	for _, x := range h {
		out = append(out, x.Key)
	}
	return out
}

// The "Download album" pill drawn again costs no more allocations than
// drawing its four textures: its ring, the glyph's ring and letter, and the
// words. Nothing is made for it each frame.
func TestAlbumPillAllocatesOnlyToDraw(t *testing.T) {
	u, _ := uitest.NewContext(t, theme.DeviceW, theme.DeviceH)
	dot := raster.Shape{W: 6, H: 6, Radius: 3, Kind: raster.Fill}
	u.Shape(dot, gfx.Rect{W: 6, H: 6}, theme.Paper)
	draw := testing.AllocsPerRun(20, func() { u.Shape(dot, gfx.Rect{W: 6, H: 6}, theme.Paper) })
	box := gfx.Rect{X: screens.Side, Y: resultsTop, W: theme.ScreenW - 2*screens.Side, H: resultH}
	drawAlbumPill(u, box, 0.5)
	if n := testing.AllocsPerRun(20, func() { drawAlbumPill(u, box, 0.5) }); n > 4*draw {
		t.Fatalf("the pill allocates %v times a frame; drawing a texture takes %v", n, draw)
	}
}

func TestResultsWithNone(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"zzqx All": nil}}
	uitest.Golden(t, "yt-results-none", render(t, NewResults(&fakeNav{}, src, nil, "zzqx"), t0))
}

func TestResultsKeepFocusPerFilter(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{
		"night drive All":   nightResults,
		"night drive Songs": only(nightResults, ytmusic.SongItem),
	}}
	r := NewResults(&fakeNav{}, src, nightStates, "night drive")
	render(t, r, t0)
	r.Handle(press(input.Down), t0)
	r.Handle(press(input.R2), t0) // Songs, still on its first row
	if got := r.views[ytmusic.Songs].Focus(); got != 0 {
		t.Fatalf("Songs opened on row %d", got)
	}
	r.Handle(press(input.Down), t0)
	r.Handle(press(input.Down), t0)
	r.Handle(press(input.L2), t0)
	if got := r.views[ytmusic.All].Focus(); got != 1 {
		t.Fatalf("All came back on row %d, want 1", got)
	}
	r.Handle(press(input.R2), t0)
	if got := r.views[ytmusic.Songs].Focus(); got != 2 {
		t.Fatalf("Songs came back on row %d, want 2", got)
	}
}

func TestResultsStates(t *testing.T) {
	cases := []struct {
		st   ytmusic.State
		want string
	}{
		{ytmusic.Waiting, "Searching…"},
		{ytmusic.Offline, stateText(ytmusic.Offline)},
		{ytmusic.Failed, stateText(ytmusic.Failed)},
		{ytmusic.Changed, stateText(ytmusic.Changed)},
	}
	for _, c := range cases {
		r := NewResults(&fakeNav{}, &fakeSource{state: c.st}, nil, "night drive")
		render(t, r, t0)
		if got := r.emptyText(r.load()); got != c.want {
			t.Errorf("state %v shows %q, want %q", c.st, got, c.want)
		}
	}
	none := NewResults(&fakeNav{}, &fakeSource{results: map[string][]ytmusic.Item{"zzqx All": nil}}, nil, "zzqx")
	render(t, none, t0)
	if got := none.emptyText(none.load()); got != "No results for “zzqx”" {
		t.Errorf("an empty answer shows %q", got)
	}
}

func TestSubtitles(t *testing.T) {
	cases := []struct {
		it     ytmusic.Item
		length bool
		want   string
	}{
		{nightResults[1], true, "Song · Lumen Coast · 3:51"},
		{nightResults[1], false, "Song · Lumen Coast"},
		{nightResults[0], true, "Album · Lumen Coast · 2023"},
		{nightResults[2], true, "Artist · 210K monthly audience"},
		{ytmusic.Item{Kind: ytmusic.AlbumItem, Type: "EP", Artist: "Vela"}, true, "EP · Vela"},
		{ytmusic.Item{Kind: ytmusic.ArtistItem}, true, "Artist"},
	}
	for _, c := range cases {
		if got := subtitle(c.it, c.length); got != c.want {
			t.Errorf("subtitle(%q, %v) = %q, want %q", c.it.Title, c.length, got, c.want)
		}
	}
}
