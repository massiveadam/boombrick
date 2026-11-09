package library

import (
	"slices"
	"testing"
)

func searchIndex() *Index {
	return NewIndex([]Album{
		{Artist: "Sleater‐Kinney", Title: "Dig Me Out", Year: 1997, Tracks: []Track{{Title: "Dig Me Out", Artist: "Sleater‐Kinney", Album: "Dig Me Out", Path: "/a/1"}}},
		{Artist: "Beyoncé", Title: "Lemonade", Year: 2016, Tracks: []Track{{Title: "Formation", Artist: "Beyoncé", Album: "Lemonade", Path: "/b/1"}}},
		{Artist: "The National", Title: "High Violet", Year: 2010, Tracks: []Track{
			{Title: "Terrible Love", Artist: "The National", Album: "High Violet", Path: "/c/1"},
			{Title: "Bloodbuzz Ohio", Artist: "The National", Album: "High Violet", Path: "/c/2"},
		}},
		{Artist: "Lovelorn", Title: "Love Songs", Year: 2001, Tracks: []Track{{Title: "Untitled", Artist: "Lovelorn", Album: "Love Songs", Path: "/d/1"}}},
	})
}

func TestSearch(t *testing.T) {
	ix := searchIndex()
	f := ix.Search("sleater kinney")
	if len(f.Artists) != 1 || len(f.Albums) != 1 || len(f.Songs) != 1 {
		t.Fatalf("punctuation: %+v", f)
	}
	if f := ix.Search("BEYONCE"); len(f.Artists) != 1 || f.Artists[0].Name != "Beyoncé" {
		t.Fatalf("accents and case: %+v", f.Artists)
	}
	if f := ix.Search("national ohio"); len(f.Songs) != 1 || f.Songs[0].Title != "Bloodbuzz Ohio" {
		t.Fatalf("words across fields: %+v", f.Songs)
	}
	// "love": the title starting with it first, then a word starting with it.
	f = ix.Search("love")
	var titles []string
	for _, s := range f.Songs {
		titles = append(titles, s.Title)
	}
	if !slices.Equal(titles, []string{"Terrible Love", "Untitled"}) {
		t.Fatalf("songs for love: %v", titles)
	}
	if len(f.Albums) != 1 || f.Albums[0].Title != "Love Songs" {
		t.Fatalf("albums for love: %+v", f.Albums)
	}
	if !ix.Search("   ").Empty() || !ix.Search("zzz").Empty() {
		t.Fatal("nothing to find found something")
	}
}

func TestAlbumsBy(t *testing.T) {
	ix := searchIndex()
	names := func(as []*Album) []string {
		var out []string
		for _, a := range as {
			out = append(out, a.Title)
		}
		return out
	}
	if got := names(ix.AlbumsBy(ByYear)); !slices.Equal(got, []string{"Lemonade", "High Violet", "Love Songs", "Dig Me Out"}) {
		t.Fatalf("by year: %v", got)
	}
	if got := names(ix.AlbumsBy(ByArtist)); !slices.Equal(got, []string{"Lemonade", "Love Songs", "Dig Me Out", "High Violet"}) {
		t.Fatalf("by artist: %v", got)
	}
	if ParseAlbumOrder(ByAdded.String()) != ByAdded || ByTitle.Next(-1) != ByYear {
		t.Fatal("order names or stepping")
	}
	if Initial("‘Ello") != 'e' || Initial("2001") != '#' || Initial("…") != 0 {
		t.Fatal("initials")
	}
}

func BenchmarkSearch(b *testing.B) {
	var albums []Album
	for i := range 900 {
		a := Album{Artist: "Artist " + string(rune('a'+i%26)), Title: "Album Title Number " + string(rune('a'+i%26))}
		for j := range 13 {
			a.Tracks = append(a.Tracks, Track{Title: "Some Song Title Here", Artist: a.Artist, Album: a.Title, Path: string(rune(i*13 + j))})
		}
		albums = append(albums, a)
	}
	ix := NewIndex(albums)
	ix.Search("x")
	b.ResetTimer()
	for range b.N {
		ix.Search("song title")
	}
}
