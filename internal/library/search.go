package library

import (
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Found is what a search of the library matched: the artists, albums and
// songs, best first.
type Found struct {
	Artists []*Artist
	Albums  []*Album
	Songs   []*Track
}

// Empty reports whether nothing matched.
func (f Found) Empty() bool { return len(f.Artists)+len(f.Albums)+len(f.Songs) == 0 }

// How many of each a search returns, at most.
const (
	foundArtists = 5
	foundAlbums  = 25
	foundSongs   = 50
)

// Search finds the artists, albums and songs matching query: every word of it
// in the name, the album's artist or the song's artist, ignoring
// case, accents and punctuation. Names that start with the query come first,
// then those with a word that does, then the rest; each in the library's own
// order.
func (ix *Index) Search(query string) Found {
	words := strings.Fields(Fold(query))
	if ix == nil || len(words) == 0 {
		return Found{}
	}
	q := strings.Join(words, " ")
	if ix.keys == nil {
		ix.keys = &searchKeys{
			artists: foldAll(ix.Artists, func(a *Artist) (string, string) { return a.Name, "" }),
			albums:  foldAll(ix.Albums, func(a *Album) (string, string) { return a.Title, a.Artist }),
			songs:   foldAll(ix.Songs, func(s *Track) (string, string) { return s.Title, s.Artist }),
		}
	}
	var f Found
	f.Artists = best(ix.Artists, ix.keys.artists, q, words, foundArtists)
	f.Albums = best(ix.Albums, ix.keys.albums, q, words, foundAlbums)
	f.Songs = best(ix.Songs, ix.keys.songs, q, words, foundSongs)
	return f
}

// searchKeys are the index's names folded for Search, made on the first one:
// an item's name, and its name with the other words it is found by.
type searchKeys struct{ artists, albums, songs []searchKey }

type searchKey struct{ name, all string }

func foldAll[T any](items []T, name func(T) (string, string)) []searchKey {
	keys := make([]searchKey, len(items))
	for i, it := range items {
		n, also := name(it)
		keys[i].name = Fold(n)
		keys[i].all = keys[i].name + " " + Fold(also)
	}
	return keys
}

// best returns up to n of items matching, best first, by their keys.
func best[T any](items []T, keys []searchKey, q string, words []string, n int) []T {
	var ranks [3][]T
	for i, it := range items {
		t := keys[i].name
		if !all(keys[i].all, words) {
			continue
		}
		r := 2
		switch {
		case strings.HasPrefix(t, q):
			r = 0
		case strings.Contains(" "+t, " "+q):
			r = 1
		}
		if len(ranks[r]) < n {
			ranks[r] = append(ranks[r], it)
		}
	}
	out := slices.Concat(ranks[0], ranks[1], ranks[2])
	return out[:min(len(out), n)]
}

// all reports whether every word is in s.
func all(s string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}

var unaccent = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// Fold makes s comparable for searching: lower case, without accents, with
// punctuation as spaces and runs of spaces as one. "Sleater‐Kinney" and
// "sleater kinney" fold alike; so do "Beyoncé" and "beyonce".
func Fold(s string) string {
	if t, _, err := transform.String(unaccent, s); err == nil {
		s = t
	}
	var b strings.Builder
	space := true // no leading space
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			space = false
		case r == '\'' || r == '’':
			// "don't" is "dont"
		case !space:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// Initial is the letter s is listed under, for jumping through a list: its
// first letter or digit, folded, or 0 for none.
func Initial(s string) rune {
	for _, r := range Fold(s) {
		if unicode.IsDigit(r) {
			return '#'
		}
		return r
	}
	return 0
}

// AlbumOrder is how the Library lists albums.
type AlbumOrder int

const (
	ByTitle  AlbumOrder = iota // A to Z
	ByArtist                   // by artist, each artist's oldest first
	ByAdded                    // most recently added first
	ByYear                     // newest first
	albumOrders
)

// AlbumOrders are the orders, as Settings and the quick menu show them.
var AlbumOrders = [albumOrders]string{"A–Z", "Artist", "Recently added", "Year"}

// Next is the order step after o, wrapping.
func (o AlbumOrder) Next(step int) AlbumOrder {
	return AlbumOrder(((int(o)+step)%int(albumOrders) + int(albumOrders)) % int(albumOrders))
}

func (o AlbumOrder) String() string {
	if o < 0 || o >= albumOrders {
		return AlbumOrders[ByTitle]
	}
	return AlbumOrders[o]
}

// ParseAlbumOrder reads an order from its name, as saved; unknown names are
// A to Z.
func ParseAlbumOrder(s string) AlbumOrder {
	for i, n := range AlbumOrders {
		if n == s {
			return AlbumOrder(i)
		}
	}
	return ByTitle
}

// AlbumsBy lists the albums in order o. A to Z and Recently added are the
// index's own lists; the others are sorted once and kept.
func (ix *Index) AlbumsBy(o AlbumOrder) []*Album {
	switch o {
	case ByArtist:
		if ix.byArtist == nil {
			ix.byArtist = slices.Clone(ix.Albums)
			slices.SortStableFunc(ix.byArtist, func(x, y *Album) int {
				if c := Compare(x.Artist, y.Artist); c != 0 {
					return c
				}
				if x.Year != y.Year {
					return x.Year - y.Year
				}
				return Compare(x.Title, y.Title)
			})
		}
		return ix.byArtist
	case ByAdded:
		return ix.Recent
	case ByYear:
		if ix.byYear == nil {
			ix.byYear = slices.Clone(ix.Albums)
			slices.SortStableFunc(ix.byYear, func(x, y *Album) int { return y.Year - x.Year })
		}
		return ix.byYear
	}
	return ix.Albums
}
