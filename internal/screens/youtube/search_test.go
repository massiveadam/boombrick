package youtube

import (
	"slices"
	"testing"

	"github.com/ujjwal313/boombrick/internal/media"
	"github.com/ujjwal313/boombrick/internal/platform/input"
	"github.com/ujjwal313/boombrick/internal/screens"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/anim"
	"github.com/ujjwal313/boombrick/internal/ui/uitest"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
	"github.com/ujjwal313/boombrick/internal/ytmusic"
)

func TestTyping(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"ni All": nightResults}}
	nav := &fakeNav{}
	s := NewSearch(nav, src, nil)
	render(t, s, t0)
	// Down to "h", along to "n", type it; back to "i", type it.
	for _, b := range []input.Button{input.Down, input.Right, input.Right, input.Right, input.Right, input.Right,
		input.Right, input.A, input.Left, input.Left, input.Left, input.Left, input.Left, input.A} {
		s.Handle(press(b), t0)
	}
	if s.query != "ni" {
		t.Fatalf("typed %q", s.query)
	}
	s.Handle(press(input.X), t0)
	s.Handle(press(input.X), t0)
	if s.query != "ni " {
		t.Fatalf("spaces: %q", s.query)
	}
	s.Handle(press(input.B), t0)
	if s.query != "ni" {
		t.Fatalf("delete: %q", s.query)
	}

	render(t, s, t0)
	if len(s.shown) != topResults || !slices.Contains(src.searched, "ni All") {
		t.Fatalf("%d results shown, asked %q", len(s.shown), src.searched)
	}
	if !s.Claims(input.Start) || s.Claims(input.Select) {
		t.Fatal("Start is claimed only to search")
	}
	s.Handle(press(input.Start), t0)
	if r, ok := nav.pushed[0].(*Results); !ok || r.query != "ni" {
		t.Fatalf("Start opened %#v", nav.pushed[0])
	}

	s.Handle(press(input.Y), t0)
	s.Handle(press(input.A), t0) // the album at the top
	if _, ok := nav.pushed[1].(*AlbumPage); !ok {
		t.Fatalf("A on the album opened %T", nav.pushed[1])
	}
	s.Handle(press(input.Down), t0)
	s.Handle(press(input.Down), t0)
	s.Handle(press(input.A), t0) // the artist
	if _, ok := nav.pushed[2].(*ArtistPage); !ok {
		t.Fatalf("A on the artist opened %T", nav.pushed[2])
	}
	s.Handle(press(input.B), t0)
	if s.onResults {
		t.Fatal("B didn't go back to the keyboard")
	}
	s.Handle(press(input.A), t0) // focus is back on "i"
	if s.query != "nii" {
		t.Fatalf("typed %q", s.query)
	}
}

func TestAnEmptyQuery(t *testing.T) {
	s := NewSearch(&fakeNav{}, &fakeSource{}, nil)
	render(t, s, t0)
	for _, b := range []input.Button{input.B, input.X, input.Y, input.Start} {
		if s.Handle(press(b), t0) {
			t.Errorf("%v did something with no query", b)
		}
	}
	if s.Claims(input.Start) {
		t.Fatal("Start claimed with nothing to search")
	}
}

func TestTypingScreen(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	s := NewSearch(&fakeNav{}, src, nightStates)
	uitest.Golden(t, "yt-search-empty", render(t, s, t0))
	s.query = "night drive"
	uitest.Golden(t, "yt-search", render(t, s, t0))
	s.Handle(press(input.Y), t0)
	s.Handle(press(input.Down), t0)
	uitest.Golden(t, "yt-search-results", render(t, s, t0.Add(anim.Focus)))
}

func TestPlayingFromTheTopResults(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	nav, dl := &fakeNav{}, &recorder{}
	s := NewSearch(nav, src, dl)
	s.query = "night drive"
	render(t, s, t0)
	for _, b := range []input.Button{input.Y, input.Down, input.A, input.X, input.Y} {
		s.Handle(press(b), t0)
	}
	if !slices.Equal(dl.played, []string{"v1@0"}) || len(dl.got) != 1 || !slices.Equal(dl.got[0], []string{"v1"}) ||
		len(dl.queued) != 1 || !slices.Equal(dl.queued[0], []string{"v1"}) || !s.onResults {
		t.Fatalf("played %q, got %q, queued %q, on the results %v", dl.played, dl.got, dl.queued, s.onResults)
	}
	if want := []string{"Downloading “Night Drive Theory”", screens.AddedToQueue}; !slices.Equal(nav.toasts, want) {
		t.Fatalf("toasts %q, want %q", nav.toasts, want)
	}
	// Y on the album and the artist does nothing.
	for _, b := range []input.Button{input.Up, input.Y, input.Down, input.Down, input.Y} {
		s.Handle(press(b), t0)
	}
	if len(dl.queued) != 1 || len(nav.toasts) != 2 || len(nav.pushed) != 0 || !s.onResults {
		t.Fatalf("queued %q, toasts %q, pushed %d, on the results %v", dl.queued, nav.toasts, len(nav.pushed), s.onResults)
	}
}

// A song already downloaded isn't asked for again.
func TestDownloadingASavedSong(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	nav, dl := &fakeNav{}, &recorder{fakeDownloads: fakeDownloads{"v1": {media.Saved, 0}}}
	s := NewSearch(nav, src, dl)
	s.query = "night drive"
	render(t, s, t0)
	for _, b := range []input.Button{input.Y, input.Down, input.X} {
		s.Handle(press(b), t0)
	}
	if len(dl.got) != 0 || !slices.Equal(nav.toasts, []string{"Already downloaded"}) {
		t.Fatalf("got %q, toasts %q", dl.got, nav.toasts)
	}
}

func TestDownloadingAnAlbumFromTheTopResults(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults},
		albums: map[string]ytmusic.AlbumPage{"MPREb_nd": nightDrive}}
	nav, dl := &fakeNav{}, &recorder{}
	s := NewSearch(nav, src, dl)
	s.query = "night drive"
	render(t, s, t0)
	s.Handle(press(input.Y), t0)
	if !s.Handle(press(input.X), t0) || len(dl.got) != 0 {
		t.Fatalf("X on the album: got %q before its page was drawn", dl.got)
	}
	render(t, s, t0)
	if len(dl.got) != 1 || len(dl.got[0]) != 5 || !slices.Equal(nav.toasts, []string{"Downloading 5 songs"}) {
		t.Fatalf("got %q, toasts %q", dl.got, nav.toasts)
	}
}

func TestTopResultHintsFit(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"night drive All": nightResults}}
	for _, dl := range []Downloads{nil, &recorder{}} {
		s := NewSearch(&fakeNav{}, src, dl)
		s.query = "night drive"
		render(t, s, t0)
		s.Handle(press(input.Y), t0)
		u, _ := uitest.NewContext(t, 64, 64)
		for i := range topResults {
			h := s.hints()
			if w := widgets.HintsWidth(u, h); w > widgets.MaxHintsWidth || len(h) == 0 {
				t.Errorf("row %d: hints %v are %v wide", i, h, w)
			}
			if theme.Brick && dl != nil && s.shown[i].Kind == ytmusic.SongItem && !slices.Equal(keys(h), []string{"A", "X", "Y", "B"}) {
				t.Errorf("a song's hints are %v", h)
			}
			s.Handle(press(input.Down), t0)
		}
	}
}

// Offline, the tab says so, and A opens Downloads instead of typing.
func TestOffline(t *testing.T) {
	nav := &fakeNav{}
	s := NewSearch(nav, &fakeSource{}, nil)
	uitest.Golden(t, "yt-offline", render(t, s, t0, func(u *ui.Context) { u.Status.Online = false }))
	if !s.Handle(press(input.A), t0) || s.query != "" || len(nav.tabs) != 1 || nav.tabs[0] != screens.DownloadsTab {
		t.Fatalf("A while offline: query %q, tabs shown %v", s.query, nav.tabs)
	}
}

func TestHintsFit(t *testing.T) {
	u, _ := uitest.NewContext(t, 64, 64)
	s := NewSearch(&fakeNav{}, &fakeSource{}, nil)
	s.shown = nightResults[:topResults]
	if w := widgets.HintsWidth(u, s.hints()); w > widgets.MaxHintsWidth {
		t.Fatalf("the keyboard's hints are %v wide", w)
	}
}

func TestClaimsFollowFocus(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{"ni All": nightResults}}
	s := NewSearch(&fakeNav{}, src, nil)
	s.query = "ni"
	render(t, s, t0)
	if !s.Claims(input.Start) {
		t.Fatal("Start not claimed on the keyboard")
	}
	s.Handle(press(input.Y), t0)
	if s.Claims(input.Start) {
		t.Fatal("Start claimed on the results, where it does nothing")
	}
	s.Handle(press(input.B), t0)
	if !s.Claims(input.Start) {
		t.Fatal("Start not claimed back on the keyboard")
	}
}

func TestLateAnswerKeepsFocus(t *testing.T) {
	src := &fakeSource{results: map[string][]ytmusic.Item{
		"ni All": nightResults, "nig All": nightResults[:2], "nigh All": nil,
	}}
	s := NewSearch(&fakeNav{}, src, nil)
	s.query = "ni"
	render(t, s, t0)
	s.Handle(press(input.Y), t0)
	s.Handle(press(input.Down), t0)
	s.Handle(press(input.Down), t0)
	if s.list.Focus() != 2 {
		t.Fatalf("focus on %d", s.list.Focus())
	}
	s.query = "nig"
	render(t, s, t0)
	if !s.onResults || len(s.shown) != 2 || s.list.Focus() != 1 {
		t.Fatalf("onResults %v, %d rows, focus %d", s.onResults, len(s.shown), s.list.Focus())
	}
	s.query = "nigh"
	render(t, s, t0)
	if s.onResults || len(s.shown) != 0 {
		t.Fatal("focus stayed on empty results")
	}
}
