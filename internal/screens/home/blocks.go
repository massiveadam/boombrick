package home

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/ujjwal313/boombrick/internal/fixture"
	"github.com/ujjwal313/boombrick/internal/history"
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/screens"
)

// The blocks Home can show, as Settings' Home page names and orders them.
const (
	BlockContinue   = "continue"
	BlockPlayed     = "recently_played"
	BlockAdded      = "recently_added"
	BlockRediscover = "rediscover"
	BlockMost       = "most_played"
	BlockRandom     = "random"
	BlockThisDay    = "on_this_day"
	BlockSpotify    = "spotify"
)

// Block is a block of Home, as Settings lists it.
type Block struct {
	ID, Label string
	On        bool
}

// blockLabels are the blocks there are, in the order Home starts with.
var blockLabels = []Block{
	{ID: BlockContinue, Label: "Continue listening"},
	{ID: BlockPlayed, Label: "Recently played"},
	{ID: BlockAdded, Label: "Recently added"},
	{ID: BlockRediscover, Label: "Rediscover"},
	{ID: BlockMost, Label: "Most played"},
	{ID: BlockRandom, Label: "Random albums"},
	{ID: BlockThisDay, Label: "On this day"},
	{ID: BlockSpotify, Label: "Spotify playlists"},
}

// Blocks reads the setting - the blocks' IDs in order, "!" before one switched
// off - into every block there is, in that order. Blocks the setting doesn't
// name, as ones added since, follow it, on.
func Blocks(setting string) []Block {
	var out []Block
	for _, f := range strings.Split(setting, ",") {
		id, off := strings.CutPrefix(strings.TrimSpace(f), "!")
		i := slices.IndexFunc(blockLabels, func(b Block) bool { return b.ID == id })
		if i < 0 || slices.ContainsFunc(out, func(b Block) bool { return b.ID == id }) {
			continue
		}
		b := blockLabels[i]
		b.On = !off
		out = append(out, b)
	}
	for _, b := range blockLabels {
		if !slices.ContainsFunc(out, func(o Block) bool { return o.ID == b.ID }) {
			b.On = true
			out = append(out, b)
		}
	}
	return out
}

// BlocksSetting is the setting for blocks: Blocks' inverse.
func BlocksSetting(blocks []Block) string {
	f := make([]string, len(blocks))
	for i, b := range blocks {
		f[i] = b.ID
		if !b.On {
			f[i] = "!" + b.ID
		}
	}
	return strings.Join(f, ",")
}

// maxBlock is how many albums a history block shows.
const maxBlock = 12

// rediscoverAfter is how long an album has gone unheard to be rediscovered,
// and rediscoverPlays how many plays make it one that was liked.
const (
	rediscoverAfter = 90 * 24 * time.Hour
	rediscoverPlays = 3
)

// albumPlays is an album's plays, added up over its files.
type albumPlays struct {
	album *library.Album
	plays int
	last  time.Time
}

// byAlbum adds up h's plays of the files keep accepts, by album in ix.
func byAlbum(h *history.History, ix *library.Index, keep func(path string) bool) []albumPlays {
	at := map[*library.Album]int{}
	var out []albumPlays
	for path, t := range h.Tracks {
		if keep != nil && !keep(path) {
			continue
		}
		a := ix.AlbumOf(path)
		if a == nil {
			continue // gone from the library
		}
		i, ok := at[a]
		if !ok {
			i = len(out)
			at[a] = i
			out = append(out, albumPlays{album: a})
		}
		out[i].plays += t.Plays
		if t.Last.After(out[i].last) {
			out[i].last = t.Last
		}
	}
	return out
}

// HistoryRow is one of the blocks made from listening history (Recently
// played, Rediscover, Most played, On this day); false when it has nothing,
// as before any music has been heard.
func HistoryRow(id string, h *history.History, ix *library.Index, now time.Time, play, queue func(*library.Album)) (fixture.Row, bool) {
	if h == nil || ix == nil {
		return fixture.Row{}, false
	}
	var title, meta string
	var albums []albumPlays
	switch id {
	case BlockPlayed:
		title, albums = "Recently played", byAlbum(h, ix, nil)
		slices.SortFunc(albums, func(a, b albumPlays) int { return b.last.Compare(a.last) })
	case BlockMost:
		title, albums = "Most played", byAlbum(h, ix, nil)
		slices.SortStableFunc(albums, func(a, b albumPlays) int { return b.plays - a.plays })
	case BlockRediscover:
		title, meta = "Rediscover", "Loved, not heard in 3 months"
		for _, a := range byAlbum(h, ix, nil) {
			if a.plays >= rediscoverPlays && now.Sub(a.last) >= rediscoverAfter {
				albums = append(albums, a)
			}
		}
		slices.SortStableFunc(albums, func(a, b albumPlays) int { return b.plays - a.plays })
	case BlockThisDay:
		title, meta = "On this day", "Played "+now.Format("January 2")+" in years past"
		albums = byAlbum(h, ix, func(p string) bool { return h.ThisDay[p] })
		slices.SortStableFunc(albums, func(a, b albumPlays) int { return b.plays - a.plays })
	default:
		return fixture.Row{}, false
	}
	list := make([]*library.Album, 0, min(len(albums), maxBlock))
	for _, a := range albums[:min(len(albums), maxBlock)] {
		list = append(list, a.album)
	}
	return albumsRow(title, meta, list, play, queue)
}

// RandomRow is Random albums: a fresh draw each time Home is made.
func RandomRow(ix *library.Index, r *rand.Rand, play, queue func(*library.Album)) (fixture.Row, bool) {
	if ix == nil || len(ix.Albums) == 0 {
		return fixture.Row{}, false
	}
	pick := r.Perm(len(ix.Albums))[:min(len(ix.Albums), maxBlock)]
	list := make([]*library.Album, len(pick))
	for i, j := range pick {
		list[i] = ix.Albums[j]
	}
	return albumsRow("Random albums", fmt.Sprintf("From %d albums", len(ix.Albums)), list, play, queue)
}

// albumsRow is a row of albums; false with none.
func albumsRow(title, meta string, albums []*library.Album, play, queue func(*library.Album)) (fixture.Row, bool) {
	if len(albums) == 0 {
		return fixture.Row{}, false
	}
	items := make([]fixture.Item, len(albums))
	for i, a := range albums {
		items[i] = fixture.Item{Title: a.Title, Subtitle: a.Artist, Art: screens.AlbumArt(a), Source: source(a),
			Play: func() { play(a) }}
		if queue != nil {
			items[i].Queue = func() { queue(a) }
		}
	}
	return fixture.Row{Title: title, Meta: meta, Items: items}, true
}
