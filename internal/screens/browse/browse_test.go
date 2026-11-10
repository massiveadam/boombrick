package browse

import (
	"fmt"
	"image"
	"slices"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/fixture"
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/screens"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui/anim"
	"github.com/ujjwal313/boombrick/internal/ui/uitest"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
)

func TestMain(m *testing.M) { uitest.Main(m) }

var t0 = time.Unix(1000, 0)

func press(b input.Button) input.Event { return input.Event{Button: b, Kind: input.Press} }

// fakePlayer records what the screens ask of the player.
type fakePlayer struct {
	played   []string // first path of each Play
	lists    [][]string
	start    []int
	queued   [][]string
	shuffled []bool
	playing  string
}

func (p *fakePlayer) State() player.State {
	return player.State{Queued: p.playing != "", Track: player.Track{Path: p.playing}}
}
func (p *fakePlayer) Play(ts []player.Track, start int) {
	p.played, p.start = append(p.played, ts[0].Path), append(p.start, start)
	p.lists = append(p.lists, paths(ts))
}
func (p *fakePlayer) SetShuffle(on bool)        { p.shuffled = append(p.shuffled, on) }
func (p *fakePlayer) Enqueue(ts []player.Track) { p.queued = append(p.queued, paths(ts)) }

func paths(ts []player.Track) []string {
	var paths []string
	for _, t := range ts {
		paths = append(paths, t.Path)
	}
	return paths
}

// fakeNav records pushes and toasts.
type fakeNav struct {
	pushed []screens.Screen
	toasts []string
	pops   int
}

func (n *fakeNav) Push(s screens.Screen) { n.pushed = append(n.pushed, s) }
func (n *fakeNav) Pop()                  { n.pops++ }
func (n *fakeNav) Toast(msg string)      { n.toasts = append(n.toasts, msg) }

// sampleIndex is a small library with names from the design.
func sampleIndex() *library.Index { return library.NewIndex(sampleAlbums()) }

// genreIndex is sampleIndex with genres tagged: Folk, Jazz, Pop and Rock.
func genreIndex() *library.Index {
	albums := sampleAlbums()
	tag := func(album, track int, genres ...string) { albums[album].Tracks[track].Genres = genres }
	tag(0, 0, "Rock")         // Rust
	tag(0, 1, "Rock", "Pop")  // Ember
	tag(0, 2, "Folk")         // Tin Roof
	tag(1, 0, "Jazz", "Rock") // Neon
	tag(2, 0, "Folk")         // Salt
	return library.NewIndex(albums)
}

// rockSongs are genreIndex's Rock songs, by artist.
var rockSongs = []string{"/m/a/1.flac", "/m/Copper Season/Rust.flac", "/m/Copper Season/Ember.flac"}

func sampleAlbums() []library.Album {
	track := func(album, title string, n int, d time.Duration) library.Track {
		return library.Track{Path: "/m/" + album + "/" + title + ".flac", Title: title, Artist: "Odalys", Album: album,
			Number: n, Duration: d}
	}
	return []library.Album{
		{Artist: "Odalys", Title: "Copper Season", Year: 2019, Duration: 15 * time.Minute, Tracks: []library.Track{
			track("Copper Season", "Rust", 1, 222*time.Second), track("Copper Season", "Ember", 2, 245*time.Second),
			track("Copper Season", "Tin Roof", 3, 178*time.Second), track("Copper Season", "Low Tide", 4, 210*time.Second),
		}},
		{Artist: "Kenji Arata", Title: "Afterhours", Year: 2021, Duration: 4 * time.Minute, Tracks: []library.Track{
			{Path: "/m/a/1.flac", Title: "Neon", Artist: "Kenji Arata", Album: "Afterhours", Duration: 4 * time.Minute},
		}},
		{Artist: "Odalys", Title: "Northbound", Year: 2016, Tracks: []library.Track{
			{Path: "/m/n/1.flac", Title: "Salt", Artist: "Odalys", Album: "Northbound"},
		}},
	}
}

func render(t *testing.T, s screens.Screen, now time.Time) *image.NRGBA {
	t.Helper()
	u, off := uitest.NewContext(t, theme.DeviceW, theme.DeviceH)
	u.Accent = theme.Derive(fixture.PlayingHue, fixture.PlayingChroma)
	u.Status = fixture.Status()
	u.Now = now
	s.Draw(u)
	return off.Image()
}

// fadesSoftly fails if the frame steps sharply anywhere on the line from one
// device pixel to another, as a focus glow cut off by a clip does.
func fadesSoftly(t *testing.T, img *image.NRGBA, from, to image.Point) {
	t.Helper()
	if p, found := uitest.HardEdge(img, from, to); found {
		t.Fatalf("a hard edge at %v, between %v and %v", p, from, to)
	}
}

// The first track sits just under the tabs; its glow fades out under them.
func TestAlbumPageFirstTrackGlow(t *testing.T) {
	pg := NewAlbumPage(&fakeNav{}, &fakePlayer{}, sampleIndex(), sampleIndex().Album("Odalys", "Copper Season"))
	pg.Handle(press(input.Right), t0)
	pg.Handle(press(input.Right), t0) // the first track
	img := render(t, pg, t0.Add(anim.Focus))
	uitest.Golden(t, "album-first-track", img)
	// Between the YouTube and Downloads tabs, down to the track.
	fadesSoftly(t, img, image.Pt(700, 120), image.Pt(700, 154))
}

func TestAlbumPage(t *testing.T) {
	ix := sampleIndex()
	p, nav := &fakePlayer{playing: "/m/Copper Season/Ember.flac"}, &fakeNav{}
	pg := NewAlbumPage(nav, p, ix, ix.Album("Odalys", "Copper Season"))
	uitest.Golden(t, "album", render(t, pg, t0))

	pg.Handle(press(input.A), t0) // Play: in order, from the top
	if !slices.Equal(p.shuffled, []bool{false}) || p.start[0] != 0 {
		t.Fatalf("Play pill: shuffle %v, start %v", p.shuffled, p.start)
	}
	pg.Handle(press(input.Right), t0) // Shuffle
	if got := pg.hints()[0]; got.Key != "A" || got.Label != "Shuffle" {
		t.Fatalf("A's hint on the Shuffle pill = %+v", got)
	}
	pg.Handle(press(input.A), t0)
	if p.shuffled[1] != true {
		t.Fatal("the Shuffle pill should turn shuffle on")
	}
	pg.Handle(press(input.Right), t0) // into the tracks
	if got := pg.hints()[0].Label; got != "Play" {
		t.Fatalf("A's hint on a track = %q", got)
	}
	pg.Handle(press(input.Down), t0)
	pg.Handle(press(input.A), t0)
	if p.start[2] != 1 {
		t.Fatalf("A on the second track started at %d", p.start[2])
	}
	if !slices.Equal(p.shuffled, []bool{false, true, false}) {
		t.Fatalf("shuffle set %v: A on a track plays the album in order from it", p.shuffled)
	}
	pg.Handle(press(input.Y), t0)
	if len(p.queued) != 1 || !slices.Equal(p.queued[0], []string{"/m/Copper Season/Ember.flac"}) || nav.toasts[0] != "Added to queue" {
		t.Fatalf("Y on a track queued %v, toasts %v", p.queued, nav.toasts)
	}
	img := render(t, pg, t0.Add(anim.Focus))
	uitest.Golden(t, "album-track-focused", img)
	// From the art across to the second track.
	fadesSoftly(t, img, image.Pt(295, 312), image.Pt(322, 312))
	pg.Handle(press(input.Left), t0) // back to the pills
	pg.Handle(press(input.Y), t0)
	if len(p.queued[1]) != 4 {
		t.Fatalf("Y on the pills should queue the album, queued %v", p.queued[1])
	}
}

func TestArtistPage(t *testing.T) {
	ix := sampleIndex()
	p, nav := &fakePlayer{}, &fakeNav{}
	ar := ix.Artists[1] // Odalys: Northbound (2016), Copper Season (2019)
	pg := NewArtistPage(nav, p, ar)
	img := render(t, pg, t0)
	uitest.Golden(t, "artist", img)
	// Right of the artist's name, down to the first card.
	fadesSoftly(t, img, image.Pt(300, 150), image.Pt(300, 220))
	pg.Handle(press(input.Right), t0)
	pg.Handle(press(input.A), t0)
	if len(nav.pushed) != 1 {
		t.Fatal("A should open the album")
	}
	if ap, ok := nav.pushed[0].(*AlbumPage); !ok || ap.title != "Copper Season" {
		t.Fatalf("opened %+v", nav.pushed[0])
	}
}

// odalysSongs are Odalys's songs in sampleIndex, albums oldest first.
var odalysSongs = []string{"/m/n/1.flac", "/m/Copper Season/Rust.flac", "/m/Copper Season/Ember.flac",
	"/m/Copper Season/Tin Roof.flac", "/m/Copper Season/Low Tide.flac"}

// shuffledAll fails unless p's one Play was songs, from any of them, with
// shuffle turned on.
func shuffledAll(t *testing.T, p *fakePlayer, songs []string) {
	t.Helper()
	if !slices.Equal(p.shuffled, []bool{true}) || len(p.lists) != 1 || !slices.Equal(p.lists[0], songs) {
		t.Fatalf("shuffle %v, played %q; want shuffle on and %q", p.shuffled, p.lists, songs)
	}
	if s := p.start[0]; s < 0 || s >= len(songs) {
		t.Fatalf("started at %d of %d", s, len(songs))
	}
}

// X on an artist's page shuffles all their songs, whichever album is focused.
func TestArtistPageShufflesAll(t *testing.T) {
	p := &fakePlayer{}
	pg := NewArtistPage(&fakeNav{}, p, sampleIndex().Artists[1])
	pg.Handle(press(input.Right), t0)
	if !pg.Handle(press(input.X), t0) {
		t.Fatal("X should be taken")
	}
	shuffledAll(t, p, odalysSongs)
	if got := pg.hints()[1]; got.Key != "X" || got.Label != "Shuffle all" {
		t.Fatalf("the second hint is %+v", got)
	}
}

// X on an artist in the Library shuffles all their songs; it does nothing on
// albums or songs.
// X searches the library from every filter.
func TestLibrarySearches(t *testing.T) {
	nav := &fakeNav{}
	l := New(nav, &fakePlayer{}, "/m", nil)
	l.SetIndex(sampleIndex())
	for range 2 {
		if !l.Handle(press(input.X), t0) {
			t.Fatal("X should open the search")
		}
		l.Handle(press(input.R2), t0)
	}
	if len(nav.pushed) != 2 {
		t.Fatalf("pushed %d pages", len(nav.pushed))
	}
	if _, ok := nav.pushed[0].(*Search); !ok {
		t.Fatalf("X opened %T", nav.pushed[0])
	}
	if got := l.hints()[1]; got.Key != "X" || got.Label != "Search" {
		t.Fatalf("the second hint is %+v", got)
	}
}

// With songs tagged, Genres is the last filter: A opens a genre and Y queues
// it.
func TestLibraryGenres(t *testing.T) {
	p, nav := &fakePlayer{}, &fakeNav{}
	l := New(nav, p, "/m", nil)
	l.SetIndex(genreIndex())
	for range 3 {
		l.Handle(press(input.R2), t0)
	}
	if l.filter != genresFilter || l.Handle(press(input.R2), t0) {
		t.Fatal("R2 from Songs should show the genres, the last filter")
	}
	for range 3 {
		l.Handle(press(input.Down), t0) // Folk, Jazz, Pop, Rock
	}
	if got := l.hints()[1]; got.Key != "X" || got.Label != "Search" {
		t.Fatalf("the second hint is %+v", got)
	}
	l.Handle(press(input.Y), t0)
	if len(p.queued) != 1 || !slices.Equal(p.queued[0], rockSongs) || len(nav.toasts) != 1 {
		t.Fatalf("Y on Rock queued %q, toasts %v", p.queued, nav.toasts)
	}
	l.Handle(press(input.A), t0)
	if pg, ok := nav.pushed[0].(*AlbumPage); !ok || pg.title != "Rock" {
		t.Fatalf("A on Rock opened %+v", nav.pushed)
	}
	uitest.Golden(t, "library-genres", render(t, l, t0.Add(anim.Focus)))
}

// A library without genre tags has no Genres filter; one whose genres go
// while it shows shows the songs instead.
func TestLibraryHidesGenresWithoutTags(t *testing.T) {
	l := New(&fakeNav{}, &fakePlayer{}, "/m", nil)
	l.SetIndex(sampleIndex())
	l.Handle(press(input.R2), t0)
	l.Handle(press(input.R2), t0)
	if l.Handle(press(input.R2), t0) {
		t.Fatal("R2 on Songs should do nothing without genres")
	}
	l.SetIndex(genreIndex())
	if !l.Handle(press(input.R2), t0) || l.filter != genresFilter {
		t.Fatal("R2 on Songs should show the genres once there are some")
	}
	l.SetIndex(sampleIndex())
	if l.filter != songsFilter {
		t.Fatalf("filter %d once the genres went, want the songs", l.filter)
	}
	render(t, l, t0)
}

// When the filters' counts don't all fit, only the chosen filter shows its
// count.
func TestLibraryCountsThatDontFit(t *testing.T) {
	if !theme.Brick {
		t.Skip("the counts are made too wide for the Brick's canvas; wider screens fit them")
	}
	var albums []library.Album
	for i := range 1000 {
		a := library.Album{Artist: fmt.Sprintf("Artist %d", i%100), Title: fmt.Sprintf("Album %d", i)}
		for j := range 10 {
			a.Tracks = append(a.Tracks, library.Track{Path: fmt.Sprintf("/m/%d/%d.flac", i, j),
				Title: fmt.Sprintf("Song %d", j), Genres: []string{fmt.Sprintf("Genre %d", (i+j)%30)}})
		}
		albums = append(albums, a)
	}
	l := New(&fakeNav{}, &fakePlayer{}, "/m", nil)
	l.SetIndex(library.NewIndex(albums))
	l.Handle(press(input.R2), t0)
	img := render(t, l, t0.Add(anim.Focus))
	if l.allCounts {
		t.Fatal("1,000 albums, 100 artists, 10,000 songs and 30 genres can't all show their counts")
	}
	l.SetIndex(genreIndex())
	render(t, l, t0)
	if !l.allCounts {
		t.Fatal("a small library's counts all fit")
	}
	uitest.Golden(t, "library-one-count", img)
}

// A genre's page is laid out as an album's, its songs each with their artist.
func TestGenrePage(t *testing.T) {
	p := &fakePlayer{playing: "/m/Copper Season/Ember.flac"}
	pg := NewGenrePage(&fakeNav{}, p, genreIndex().Genres[3])
	if pg.title != "Rock" || pg.who != "2 artists" || pg.length != "3 songs · 11 min" || pg.rows[0].Subtitle != "Kenji Arata" {
		t.Fatalf("page %q, %q, %q; first row %+v", pg.title, pg.who, pg.length, pg.rows[0])
	}
	pg.Handle(press(input.A), t0) // Play
	pg.Handle(press(input.Right), t0)
	pg.Handle(press(input.A), t0) // Shuffle
	pg.Handle(press(input.Right), t0)
	pg.Handle(press(input.Down), t0)
	pg.Handle(press(input.A), t0) // the second song
	if !slices.Equal(p.shuffled, []bool{false, true, false}) || p.start[0] != 0 || p.start[2] != 1 {
		t.Fatalf("shuffle %v, starts %v", p.shuffled, p.start)
	}
	for _, l := range p.lists {
		if !slices.Equal(l, rockSongs) {
			t.Fatalf("played %q, want %q", l, rockSongs)
		}
	}
	pg.Handle(press(input.Y), t0)
	if len(p.queued) != 1 || !slices.Equal(p.queued[0], rockSongs[1:2]) {
		t.Fatalf("Y on the second song queued %q", p.queued)
	}
	if pg := NewGenrePage(&fakeNav{}, p, genreIndex().Genres[1]); pg.who != "1 artist" {
		t.Fatalf("Jazz is by %q", pg.who)
	}
	uitest.Golden(t, "genre", render(t, NewGenrePage(&fakeNav{}, p, genreIndex().Genres[3]), t0))
}

// X on an album's page opens its artist; on an album opened from that
// artist's page it goes back there instead of stacking another copy.
func TestAlbumPageArtist(t *testing.T) {
	ix := sampleIndex()
	al := ix.Album("Odalys", "Copper Season")
	nav := &fakeNav{}
	pg := NewAlbumPage(nav, &fakePlayer{}, ix, al)
	if hs := pg.hints(); hs[1] != artistHint {
		t.Fatalf("hints %v: no X for the artist after A", hs)
	}
	if !pg.Handle(press(input.X), t0) || len(nav.pushed) != 1 {
		t.Fatalf("X pushed %d pages", len(nav.pushed))
	}
	ar, ok := nav.pushed[0].(*ArtistPage)
	if !ok || ar.artist != ix.Artist("Odalys") || ar.artist == nil {
		t.Fatalf("X opened %#v, not Odalys's page", nav.pushed[0])
	}

	ar.Handle(press(input.A), t0) // an album from the artist's page
	from := nav.pushed[len(nav.pushed)-1].(*AlbumPage)
	pushed := len(nav.pushed)
	if !from.Handle(press(input.X), t0) || nav.pops != 1 || len(nav.pushed) != pushed {
		t.Fatalf("X from the artist's album: %d pops, %d new pages", nav.pops, len(nav.pushed)-pushed)
	}

	// No artist to go to: a genre's page, or a page with no library.
	for _, pg := range []*AlbumPage{NewGenrePage(nav, &fakePlayer{}, genreIndex().Genres[0]),
		NewAlbumPage(nav, &fakePlayer{}, nil, al)} {
		if pg.Handle(press(input.X), t0) || slices.Contains(pg.hints(), artistHint) {
			t.Fatalf("X on %q is offered or taken with no artist", pg.title)
		}
	}
}

func TestPageHintsFit(t *testing.T) {
	u, _ := uitest.NewContext(t, 64, 64)
	ix := sampleIndex()
	shuffle := NewAlbumPage(&fakeNav{}, &fakePlayer{}, ix, ix.Recent[0])
	shuffle.Handle(press(input.Right), t0)
	for _, hints := range [][]widgets.Hint{
		NewAlbumPage(&fakeNav{}, &fakePlayer{}, ix, ix.Recent[0]).hints(),
		shuffle.hints(),
		NewArtistPage(&fakeNav{}, &fakePlayer{}, ix.Artists[0]).hints(),
	} {
		if w := widgets.HintsWidth(u, hints); w > widgets.MaxHintsWidth {
			t.Errorf("hints %v are %.1f wide, more than %.1f", hints, w, widgets.MaxHintsWidth)
		}
	}
}

func TestLibraryFilters(t *testing.T) {
	ix := sampleIndex()
	p, nav := &fakePlayer{}, &fakeNav{}
	l := New(nav, p, "/mnt/SDCARD/Music", nil)
	l.SetIndex(ix)
	img := render(t, l, t0)
	uitest.Golden(t, "library-albums", img)
	// Between the Albums and Artists filters, down to the first card.
	fadesSoftly(t, img, image.Pt(300, 150), image.Pt(300, 216))
	l.Handle(press(input.A), t0)
	if ap, ok := nav.pushed[0].(*AlbumPage); !ok || ap.title != ix.Albums[0].Title {
		t.Fatalf("A on the first album opened %+v", nav.pushed)
	}
	if !l.Handle(press(input.R2), t0) || l.filter != artistsFilter {
		t.Fatal("R2 should show artists")
	}
	img = render(t, l, t0.Add(anim.Focus))
	uitest.Golden(t, "library-artists", img)
	// Right of the filters, down to the first row.
	fadesSoftly(t, img, image.Pt(900, 150), image.Pt(900, 225))
	l.Handle(press(input.Y), t0) // Kenji Arata's one album
	if len(p.queued) != 1 || len(p.queued[0]) != 1 {
		t.Fatalf("Y on an artist queued %v", p.queued)
	}
	l.Handle(press(input.R2), t0)
	l.Handle(press(input.Down), t0)
	l.Handle(press(input.A), t0)
	if len(p.played) != 1 || p.start[0] != 1 {
		t.Fatalf("A on the second song: played %v from %v", p.played, p.start)
	}
	if !slices.Equal(p.shuffled, []bool{false}) {
		t.Fatalf("shuffle set %v: A on a song plays the list in order from it", p.shuffled)
	}
	uitest.Golden(t, "library-songs", render(t, l, t0.Add(anim.Focus)))
	if l.Handle(press(input.R2), t0) {
		t.Fatal("R2 on the last filter does nothing")
	}
	l.Handle(press(input.L2), t0)
	l.Handle(press(input.L2), t0)
	if l.filter != albumsFilter {
		t.Fatal("L2 goes back through the filters")
	}
}

// Frames drawn again allocate nothing: rows' lines and art, counts and hint
// bars are made once.
func TestFramesDrawnAgainAllocateNothing(t *testing.T) {
	u, _ := uitest.NewContext(t, theme.DeviceW, theme.DeviceH)
	u.Status = fixture.Status()
	u.Now = t0
	ix := genreIndex()
	draw := func(name string, s screens.Screen) {
		t.Helper()
		s.Draw(u)
		if n := testing.AllocsPerRun(10, func() { s.Draw(u) }); n != 0 {
			t.Errorf("%s: a frame allocates %v times", name, n)
		}
	}
	l := New(&fakeNav{}, &fakePlayer{playing: ix.Songs[1].Path}, "/m", nil)
	l.SetIndex(ix)
	for f := range filterCount {
		l.filter = f
		draw(filterNames[f], l)
	}
	draw("an artist's page", NewArtistPage(&fakeNav{}, &fakePlayer{}, ix.Artists[0]))
	draw("an album's page", NewAlbumPage(&fakeNav{}, &fakePlayer{}, ix, ix.Albums[0]))
	draw("a genre's page", NewGenrePage(&fakeNav{}, &fakePlayer{}, ix.Genres[0]))
}

// A new index's rows are made afresh.
func TestNewIndexRowsAreMadeAfresh(t *testing.T) {
	u, _ := uitest.NewContext(t, theme.DeviceW, theme.DeviceH)
	l := New(&fakeNav{}, &fakePlayer{}, "/m", nil)
	l.SetIndex(sampleIndex())
	l.filter = songsFilter
	l.Draw(u)
	l.SetIndex(library.NewIndex([]library.Album{{Artist: "Marisol Vega", Title: "Salt & Static", Tracks: []library.Track{
		{Path: "/m/s/1.flac", Title: "Low Tide Radio", Artist: "Marisol Vega", Album: "Salt & Static", Duration: 222 * time.Second},
	}}}))
	l.Draw(u)
	if r := l.rows[songsFilter][0]; r.subtitle != "Marisol Vega · Salt & Static" || r.duration != "3:42" {
		t.Fatalf("the first song's row: %q, %q", r.subtitle, r.duration)
	}
	if l.counts != [filterCount]string{"1", "1", "1", "0"} {
		t.Fatalf("counts %q", l.counts)
	}
}

func TestEmptyLibrary(t *testing.T) {
	scans := 0
	nav := &fakeNav{}
	l := New(nav, &fakePlayer{}, "/mnt/SDCARD/Music", func() { scans++ })
	if l.Handle(press(input.Y), t0) || scans != 0 {
		t.Fatal("before the index loads, Y does nothing")
	}
	l.SetIndex(library.NewIndex(nil))
	uitest.Golden(t, "library-empty", render(t, l, t0))
	if !l.Handle(press(input.Y), t0) || scans != 1 || len(nav.toasts) != 1 {
		t.Fatalf("Y on the empty library: %d scans, toasts %v", scans, nav.toasts)
	}
}

// While the first scan runs with nothing indexed, the tab says the music is
// being read, with only the tab hint, until the index arrives.
func TestLibraryWhileReading(t *testing.T) {
	scans := 0
	l := New(&fakeNav{}, &fakePlayer{}, "/mnt/SDCARD/Music", func() { scans++ })
	if got := l.hints(); len(got) != 1 || got[0].Key != "L1/R1" {
		t.Fatalf("hints before the index loads = %v, want only L1/R1 Tabs", got)
	}
	l.SetReading()
	render(t, l, t0) // the reading state; Home's golden covers how it looks
	if got := l.hints(); len(got) != 1 || got[0].Key != "L1/R1" {
		t.Fatalf("hints while reading = %v, want only L1/R1 Tabs", got)
	}
	if l.Handle(press(input.Y), t0) || scans != 0 {
		t.Fatal("Y while reading should not rescan")
	}
	l.SetIndex(sampleIndex())
	if got := l.hints(); len(got) != 5 {
		t.Fatalf("hints once the index loads = %v", got)
	}
}

func TestLibraryHintsFit(t *testing.T) {
	u, _ := uitest.NewContext(t, 64, 64)
	l := New(&fakeNav{}, &fakePlayer{}, "", nil)
	l.SetIndex(genreIndex())
	for f := range filterCount {
		l.filter = f
		if w := widgets.HintsWidth(u, l.hints()); w > widgets.MaxHintsWidth {
			t.Errorf("filter %d hints are %.1f wide, more than %.1f", f, w, widgets.MaxHintsWidth)
		}
	}
}

func TestEmptyAlbum(t *testing.T) {
	p, nav := &fakePlayer{}, &fakeNav{}
	pg := NewAlbumPage(nav, p, nil, &library.Album{Title: "Nothing", Artist: "Nobody"})
	pg.Handle(press(input.A), t0)
	pg.Handle(press(input.Right), t0)
	pg.Handle(press(input.A), t0) // Shuffle
	pg.Handle(press(input.Y), t0)
	if len(p.played) != 0 || len(p.queued) != 0 || len(nav.toasts) != 0 {
		t.Fatalf("an empty album played %v, queued %v, toasted %v", p.played, p.queued, nav.toasts)
	}
}

func TestSongCounts(t *testing.T) {
	if got := artistLine(1, 1); got != "1 album · 1 song" {
		t.Errorf("one album, one song: %q", got)
	}
	if got := artistLine(2, 1234); got != "2 albums · 1,234 songs" {
		t.Errorf("two albums: %q", got)
	}
}

// A reload that inserts albums, artists, songs and genres ahead of the
// focused ones keeps focus on the same items.
func TestLibraryKeepsFocusAcrossReloads(t *testing.T) {
	l := New(&fakeNav{}, &fakePlayer{}, "/m", nil)
	ix := genreIndex()
	l.SetIndex(ix)
	render(t, l, t0) // lays out the album grid
	for f := range filterCount {
		l.filter = f
		l.views[f].SetFocus(l.views[f].Count()-1, t0)
	}
	album, artist, song := ix.Albums[len(ix.Albums)-1], ix.Artists[len(ix.Artists)-1], ix.Songs[len(ix.Songs)-1]
	genre := ix.Genres[len(ix.Genres)-1]

	more := []library.Album{{Artist: "Aaron", Title: "Aardvark", Tracks: []library.Track{
		{Path: "/m/aa/1.flac", Title: "Aa", Artist: "Aaron", Album: "Aardvark", Genres: []string{"Ambient"}},
	}}}
	for _, a := range ix.Recent {
		more = append(more, *a)
	}
	ix2 := library.NewIndex(more)
	l.SetIndex(ix2)
	if a := ix2.Albums[l.views[albumsFilter].Focus()]; a.Title != album.Title {
		t.Errorf("album focus moved to %q, want %q", a.Title, album.Title)
	}
	if a := ix2.Artists[l.views[artistsFilter].Focus()]; a.Name != artist.Name {
		t.Errorf("artist focus moved to %q, want %q", a.Name, artist.Name)
	}
	if s := ix2.Songs[l.views[songsFilter].Focus()]; s.Path != song.Path {
		t.Errorf("song focus moved to %q, want %q", s.Path, song.Path)
	}
	if g := ix2.Genres[l.views[genresFilter].Focus()]; g.Name != genre.Name {
		t.Errorf("genre focus moved to %q, want %q", g.Name, genre.Name)
	}
}

func TestLibraryAlbumsWithArt(t *testing.T) {
	l := New(&fakeNav{}, &fakePlayer{}, "/m", nil)
	l.SetIndex(sampleIndex())
	u, off := uitest.NewContext(t, theme.DeviceW, theme.DeviceH)
	uitest.WithCovers(u)
	u.Accent = theme.Derive(fixture.PlayingHue, fixture.PlayingChroma)
	u.Status = fixture.Status()
	for _, at := range []time.Time{t0, t0.Add(anim.ArtFade)} {
		u.BeginFrame(at)
		l.Draw(u)
	}
	uitest.Golden(t, "library-albums-art", off.Image())
}

// Typing finds the library's artists, albums and songs; Y goes to them, A
// opens an album and B goes back to the keyboard, then deletes.
func TestSearchPage(t *testing.T) {
	p, nav := &fakePlayer{}, &fakeNav{}
	ix := sampleIndex()
	s := NewSearch(nav, p, ix)
	if s.Claims(input.B) {
		t.Fatal("with nothing typed B should go back")
	}
	al := ix.Albums[0]
	for _, r := range library.Fold(al.Title)[:3] {
		s.query += string(r)
	}
	s.refresh()
	if len(s.results) == 0 {
		t.Fatalf("nothing found for %q", s.query)
	}
	if !s.Handle(press(input.Y), t0) || !s.onResults {
		t.Fatal("Y should go to the results")
	}
	i := slices.IndexFunc(s.results, func(r result) bool { return r.album == al })
	if i < 0 {
		t.Fatalf("%q not found by %q", al.Title, s.query)
	}
	s.list.SetFocus(i, t0)
	s.Handle(press(input.A), t0)
	if pg, ok := nav.pushed[len(nav.pushed)-1].(*AlbumPage); !ok || pg.title != al.Title {
		t.Fatalf("A opened %T", nav.pushed[len(nav.pushed)-1])
	}
	if !s.Claims(input.B) || !s.Handle(press(input.B), t0) || s.onResults {
		t.Fatal("B on the results should go back to the keyboard")
	}
	q := s.query
	s.Handle(press(input.B), t0)
	if s.query != q[:len(q)-1] {
		t.Fatalf("B on the keyboard should delete: %q", s.query)
	}
}

func TestJump(t *testing.T) {
	names := []string{"Abba", "Air", "Beck", "Björk", "Cake"}
	at := func(i int) rune { return library.Initial(names[i]) }
	for _, c := range []struct{ f, step, want int }{
		{0, 1, 2}, {2, 1, 4}, {4, 1, 4}, // forward to the next letter; at the end, stay
		{3, -1, 2}, {2, -1, 0}, {1, -1, 0}, {0, -1, 0}, // back to the letter's start, then the one before
	} {
		if got := jump(len(names), c.f, c.step, at); got != c.want {
			t.Errorf("jump from %d by %d = %d, want %d", c.f, c.step, got, c.want)
		}
	}
}
