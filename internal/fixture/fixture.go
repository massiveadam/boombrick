// Package fixture holds Home's row types and the sample content the screen
// tests draw.
package fixture

import (
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/media"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/widgets"
)

// The track playing in the Listening frames.
const (
	PlayingTitle  = "Low Tide Radio"
	PlayingHue    = 35.0
	PlayingChroma = 0.12
)

// Item is one card or tile on Home.
type Item struct {
	Title, Subtitle string
	Art             widgets.ArtSpec
	Source          media.Source
	Progress        float64 // 0-1, Continue listening only
	Play            func()  // starts the item's music; nil for sample content
	Queue           func()  // adds the item's music to the queue; nil for sample content
	Import          func()  // X: imports a Spotify playlist; nil for none
	// Album is the album the item plays, for Home to open its page with A
	// (X then plays it); nil for others.
	Album *library.Album
}

// Row is a titled row on Home.
type Row struct {
	Title, Meta string
	Items       []Item
	Cards       bool // 200 px cards with captions; otherwise 166 px tiles
	// Size, when set, is the items' size instead, and Caption whether they
	// carry their title and subtitle: Home's views (home.Styles).
	Size     float64
	Caption  bool
	Continue bool // cards carry a source badge and a progress strip
	Spotify  bool // A opens and X imports, instead of play and download
}

// Status is the status bar of the design frames.
func Status() ui.Status {
	return ui.Status{Time: "9:41", NowPlaying: PlayingTitle, Online: true, Bluetooth: "Kite Buds", Battery: 78}
}

func art(hue float64, variant int, glyph string) widgets.ArtSpec {
	return widgets.ArtSpec{Hue: hue, Variant: variant, Glyph: glyph}
}

// Home returns the rows of frames 01a to 01c.
func Home() []Row {
	return []Row{
		{
			Title: "Continue listening", Meta: "4 in progress", Cards: true, Continue: true,
			Items: []Item{
				{Title: "Salt & Static", Subtitle: "Marisol Vega", Art: art(35, 0, "S"), Source: media.Spotify, Progress: 0.42},
				{Title: "Northbound", Subtitle: "The Hollow Pines", Art: art(150, 1, "N"), Source: media.Downloaded, Progress: 0.70},
				{Title: "Afterhours", Subtitle: "Kenji Arata", Art: art(265, 3, "A"), Source: media.YouTube, Progress: 0.18},
				{Title: "Copper Season", Subtitle: "Odalys", Art: art(60, 2, "C"), Source: media.Local, Progress: 0.88},
			},
		},
		{
			Title: "Downloads", Meta: "214 songs · plays offline",
			Items: []Item{{Art: art(150, 1, "N")}, {Art: art(320, 3, "S")}, {Art: art(95, 0, "M")}, {Art: art(230, 2, "T")}, {Art: art(15, 1, "B")}},
		},
		{
			Title: "Spotify playlists", Meta: "18 playlists", Cards: true, Spotify: true,
			Items: []Item{
				{Title: "Liked Songs", Subtitle: "1,204 songs", Art: art(330, 2, "♥")},
				{Title: "Morning Ferry", Subtitle: "42 songs", Art: art(200, 0, "M")},
				{Title: "Rainy Commute", Subtitle: "67 songs", Art: art(240, 1, "R")},
				{Title: "Kitchen Disco", Subtitle: "88 songs", Art: art(70, 3, "K")},
			},
		},
		{
			Title: "Recently added", Meta: "This week",
			Items: []Item{{Art: art(95, 0, "M")}, {Art: art(15, 3, "B")}, {Art: art(230, 2, "T")}, {Art: art(180, 1, "H")}, {Art: art(300, 0, "L")}},
		},
	}
}
