package home

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/fixture"
	"github.com/ujjwal313/boombrick/internal/history"
	"github.com/ujjwal313/boombrick/internal/library"
)

func TestBlocksSetting(t *testing.T) {
	bs := Blocks("random,!continue,nonsense,random")
	if bs[0].ID != BlockRandom || !bs[0].On || bs[1].ID != BlockContinue || bs[1].On {
		t.Fatalf("first: %+v", bs[:2])
	}
	if len(bs) != len(blockLabels) {
		t.Fatalf("every block, once: %d", len(bs))
	}
	for _, b := range bs[2:] {
		if !b.On {
			t.Fatalf("a block the setting doesn't name is on: %+v", b)
		}
	}
	if got := Blocks(BlocksSetting(bs)); BlocksSetting(got) != BlocksSetting(bs) {
		t.Fatal("not its own inverse")
	}
	if def := Blocks(""); def[0].ID != BlockContinue || !def[0].On {
		t.Fatalf("default: %+v", def[0])
	}
}

func lib() *library.Index {
	mk := func(artist, title string, paths ...string) library.Album {
		a := library.Album{Artist: artist, Title: title}
		for _, p := range paths {
			a.Tracks = append(a.Tracks, library.Track{Path: p, Title: p})
		}
		return a
	}
	return library.NewIndex([]library.Album{
		mk("A", "Old Love", "/old/1", "/old/2"),
		mk("B", "New", "/new/1"),
		mk("C", "Once", "/once/1"),
	})
}

func TestHistoryRows(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	h := &history.History{
		Tracks: map[string]history.Track{
			"/old/1":  {Plays: 6, Last: now.AddDate(0, -6, 0)},
			"/old/2":  {Plays: 4, Last: now.AddDate(0, -5, 0)},
			"/new/1":  {Plays: 2, Last: now.Add(-time.Hour)},
			"/once/1": {Plays: 1, Last: now.AddDate(-1, 0, 0)},
			"/gone":   {Plays: 50, Last: now},
		},
		ThisDay: map[string]bool{"/once/1": true},
	}
	ix, noop := lib(), func(*library.Album) {}
	titles := func(id string) []string {
		r, ok := HistoryRow(id, h, ix, now, noop, noop)
		if !ok {
			return nil
		}
		var out []string
		for _, it := range r.Items {
			out = append(out, it.Title)
		}
		return out
	}
	eq := func(id string, want ...string) {
		t.Helper()
		got := titles(id)
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", id, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", id, got, want)
			}
		}
	}
	eq(BlockPlayed, "New", "Old Love", "Once")
	eq(BlockMost, "Old Love", "New", "Once") // 10 plays over its two files
	eq(BlockRediscover, "Old Love")          // played 3+ times, none in 3 months
	eq(BlockThisDay, "Once")
	if _, ok := HistoryRow(BlockPlayed, &history.History{Tracks: map[string]history.Track{}}, ix, now, noop, noop); ok {
		t.Fatal("a row with no history")
	}
	r, ok := RandomRow(ix, rand.New(rand.NewPCG(1, 2)), noop, noop)
	if !ok || len(r.Items) != 3 {
		t.Fatalf("random: %v %d", ok, len(r.Items))
	}
}

func TestBlockStyles(t *testing.T) {
	bs := Blocks("random:covers,!continue:small,recently_played:bogus")
	if bs[0].Style != StyleCovers || bs[1].Style != StyleSmall || bs[1].On || bs[2].Style != StyleLarge {
		t.Fatalf("styles: %+v", bs[:3])
	}
	if Blocks(BlocksSetting(bs))[0].Style != StyleCovers {
		t.Fatal("a style isn't kept")
	}
	r := Styled(fixture.Row{Cards: true}, StyleCovers)
	if r.Caption || r.Cards || r.Size != tileSize {
		t.Fatalf("covers: %+v", r)
	}
	if r := Styled(fixture.Row{}, StyleLarge); !r.Caption || r.Size != largeSize {
		t.Fatalf("large: %+v", r)
	}
}
