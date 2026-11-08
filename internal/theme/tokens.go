package theme

import "image/color"

// Colour tokens.
var (
	Ground    = Hex("#171514")
	Raised    = Hex("#22201e")
	LiftedRow = Hex("#2a2422")
	Paper     = Hex("#f3f2f2")
	Ink2      = Hex("#d7d3d3")
	Ink3      = Hex("#bab6b6")
	Ink4      = Hex("#605d5d")
	Gold      = Hex("#e1ad66")
	GoldSoft  = Hex("#facb8d")

	Scrim     = color.NRGBA{R: 10, G: 9, B: 8, A: 153} // 60 %
	HintBarBG = color.NRGBA{R: 10, G: 9, B: 8, A: 140} // 55 %
	ArtShade  = color.NRGBA{R: 10, G: 9, B: 8, A: 153} // progress strip track on cards
	BadgeBG   = color.NRGBA{R: 10, G: 9, B: 8, A: 64}  // 25 %

	Success      = OKLCH{L: 0.82, C: 0.12, H: 150}.NRGBA(1)
	Danger       = OKLCH{L: 0.78, C: 0.11, H: 25}.NRGBA(1)
	FallbackGlow = OKLCH{L: 0.30, C: 0.04, H: 75}.NRGBA(1)
)

// PaperA is paper at opacity a (0-1), the colour of hairlines, outlines and
// dimmed text.
func PaperA(a float64) color.NRGBA {
	c := Paper
	c.A = to8(a)
	return c
}

// DeviceW, DeviceH and Scale, the screen's size in device pixels and how many
// device pixels one design pixel covers, are set per device by build tag:
// screen_brick.go and screen_rgsp.go.

// Canvas size in design pixels, the unit every layout uses.
const (
	ScreenW = DeviceW / Scale
	ScreenH = DeviceH / Scale
)
