//go:build rgsp

package theme

// The Anbernic RG SP's 720 x 480 screen (per BaseOS's device list and
// plorpOS; not yet checked on hardware). The canvas keeps the Brick's height,
// 614.4 design pixels, so every screen's vertical layout is unchanged, and
// widens to 921.6 for the 3:2 panel: layouts that centre on ScreenW spread
// out, and those pinned to the left leave room on the right.
const (
	DeviceW = 720
	DeviceH = 480
	Scale   = 480.0 / 614.4
	Brick   = false
)
