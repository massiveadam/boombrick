//go:build !rgsp

// Tests of exact pixel positions and rounding on the Brick's 1024 x 768
// screen, at 1.25 device pixels to the design pixel; other screens differ.

package widgets

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/media"
	"github.com/ujjwal313/boombrick/internal/theme"
	"github.com/ujjwal313/boombrick/internal/ui"
	"github.com/ujjwal313/boombrick/internal/ui/gfx"
	"github.com/ujjwal313/boombrick/internal/ui/uitest"
)

func TestStatusBarLongTitle(t *testing.T) {
	u, off := uitest.NewContext(t, theme.DeviceW, int(StatusHeight*theme.Scale))
	u.Status = ui.Status{Time: "21:07", NowPlaying: "Paper Lanterns (Extended Session) — The Hollow Pines",
		Online: true, Bluetooth: "Kite Buds", Battery: 78}
	StatusBar(u)
	uitest.Golden(t, "status-long-title", off.Image())
}

func TestStatusBarOffline(t *testing.T) {
	u, off := uitest.NewContext(t, theme.DeviceW, int(StatusHeight*theme.Scale))
	u.Status = ui.Status{Time: "21:07", Battery: 23}
	StatusBar(u)
	uitest.Golden(t, "status-offline", off.Image())
}

// With no battery the percentage and glyph go, and "Offline" moves up to the
// right-hand padding.
func TestStatusBarWithoutBattery(t *testing.T) {
	bar := func(battery int) *image.NRGBA {
		u, off := uitest.NewContext(t, theme.DeviceW, int(StatusHeight*theme.Scale))
		u.Status = ui.Status{Time: "21:07", Battery: battery}
		StatusBar(u)
		return off.Image()
	}
	none, some := bar(-1), bar(23)
	// The right-most 40 px hold the battery glyph, or the end of "Offline".
	right := image.Rect(int((theme.ScreenW-28-40)*theme.Scale), 0, int((theme.ScreenW-28)*theme.Scale), none.Bounds().Dy())
	if brightest(some, right) < 200 {
		t.Fatal("the battery glyph should be drawn at the right")
	}
	if b := brightest(none, right); b < 120 || b >= brightest(some, right) {
		t.Fatalf("brightest at the right without a battery = %d: want the gold Offline label there", b)
	}
	if b := brightest(none, image.Rect(int((theme.ScreenW-28)*theme.Scale)+2, 0, theme.DeviceW, none.Bounds().Dy())); b > 40 {
		t.Fatalf("something is drawn in the right-hand padding: %d", b)
	}
	uitest.Golden(t, "status-no-battery", none)
}

// brightest is the highest blue value in r: 242 for paper, 141 for the gold
// Offline label and 20 for the ground.
func brightest(img *image.NRGBA, r image.Rectangle) uint8 {
	var b uint8
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			b = max(b, img.NRGBAAt(x, y).B)
		}
	}
	return b
}

func TestCoverCornersAreRounded(t *testing.T) {
	u, off := uitest.NewContext(t, 200, 200)
	u.Covers = &fakeCovers{ready: true, col: color.NRGBA{0, 200, 0, 255}}
	box := gfx.Rect{X: 20, Y: 20, W: 100, H: 100}
	for _, at := range []time.Time{time.Unix(0, 0), time.Unix(1, 0)} {
		u.BeginFrame(at)
		off.Clear(theme.Ground)
		Art(u, coverSpec("Copper Season"), 100, CardRadius, box)
	}
	img := off.Image()
	if c := img.NRGBAAt(25, 25); c != theme.Ground {
		t.Fatalf("the corner shows %v, want the ground", c)
	}
	if c := img.NRGBAAt(87, 87); c != (color.NRGBA{0, 200, 0, 255}) {
		t.Fatalf("the centre shows %v", c)
	}
}

// A download at 0 % draws no sector, not even a hairline to 12 o'clock, and
// one whose progress is NaN draws as 0 % does, making no mask each frame.
func TestDownloadCircleAtNothing(t *testing.T) {
	draw := func(p float32) *image.NRGBA {
		u, off := uitest.NewContext(t, 80, 80)
		u.Accent = theme.Derive(265, 0.12)
		DownloadCircle(u, media.Downloading, p, 44, 8, 8) // 55 device pixels from (10, 10)
		return off.Image()
	}
	zero := draw(0)
	// Straight up from the centre, between the ring and the icon.
	for y := 14; y < 23; y++ {
		for x := 36; x <= 38; x++ {
			if c := zero.NRGBAAt(x, y); c != theme.Ground {
				t.Fatalf("0 %% draws %v at (%d, %d)", c, x, y)
			}
		}
	}
	nan := float32(math.NaN())
	if !bytes.Equal(draw(nan).Pix, zero.Pix) {
		t.Fatal("NaN draws otherwise than 0 %")
	}

	u, _ := uitest.NewContext(t, 80, 80)
	DownloadCircle(u, media.Downloading, 0, 44, 8, 8)
	DownloadCircle(u, media.Downloading, nan, 44, 8, 8)
	base := testing.AllocsPerRun(20, func() { DownloadCircle(u, media.Downloading, 0, 44, 8, 8) })
	if n := testing.AllocsPerRun(20, func() { DownloadCircle(u, media.Downloading, nan, 44, 8, 8) }); n > base {
		t.Fatalf("NaN allocates %v times a frame, 0 %% %v: a mask each frame", n, base)
	}
}

func TestFiltersMeasureWhatTheyDraw(t *testing.T) {
	u, _ := uitest.NewContext(t, theme.DeviceW, 100)
	names := []string{"All", "Songs", "Albums", "Artists"}
	for _, counts := range [][]string{nil, {"40", "20", "12", "3"}} {
		if w, drawn := FiltersWidth(u, names, counts), Filters(u, names, counts, 1, 32, 0); w != drawn {
			t.Errorf("counts %v: measured %v, drawn %v", counts, w, drawn)
		}
	}
}
