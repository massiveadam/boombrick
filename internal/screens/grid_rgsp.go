//go:build rgsp

package screens

// The RG SP's album grid: its 3:2 canvas (921.6 design pixels wide) takes
// four 190 px cards, 32 apart, which leaves the grid's sides at the pages'
// own 32 px margin.
const (
	GridCols = 4
	GridArt  = 190
	GridGapX = 32
)
