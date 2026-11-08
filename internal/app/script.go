package app

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"time"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/ujjwal313/boombrick/internal/platform/input"
)

// A script is a desktop test hook, $BOOMBRICK_SCRIPT: steps run one after
// another, separated by spaces, so the app can be driven and photographed
// without a keyboard, as under Xvfb.
//
//	500ms, 2s      wait
//	A, Down, L1…   press and let go of a button (names as in scriptButtons)
//	shot:FILE.png  save the next frame drawn
//	quit           quit
//
// Buttons pass through dispatch, as the device's own do.
type scriptStep struct {
	wait   time.Duration
	button input.Button
	shot   string
	quit   bool
}

var scriptButtons = map[string]input.Button{
	"Up": input.Up, "Down": input.Down, "Left": input.Left, "Right": input.Right,
	"A": input.A, "B": input.B, "X": input.X, "Y": input.Y,
	"L1": input.L1, "R1": input.R1, "L2": input.L2, "R2": input.R2,
	"Start": input.Start, "Select": input.Select, "Menu": input.Menu,
}

func parseScript(s string) ([]scriptStep, error) {
	var steps []scriptStep
	for _, f := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
		switch {
		case f == "quit":
			steps = append(steps, scriptStep{quit: true})
		case strings.HasPrefix(f, "shot:"):
			steps = append(steps, scriptStep{shot: strings.TrimPrefix(f, "shot:")})
		default:
			if b, ok := scriptButtons[f]; ok {
				steps = append(steps, scriptStep{button: b})
				continue
			}
			d, err := time.ParseDuration(f)
			if err != nil {
				return nil, fmt.Errorf("script step %q", f)
			}
			steps = append(steps, scriptStep{wait: d})
		}
	}
	return steps, nil
}

// runScript feeds the steps to the loop, waiting between them.
func (a *App) runScript(steps []scriptStep) {
	for _, st := range steps {
		if st.wait > 0 {
			select {
			case <-time.After(st.wait):
			case <-a.stop:
				return
			}
			continue
		}
		select {
		case a.script <- st:
			a.wake()
		case <-a.stop:
			return
		}
		if st.button != input.None {
			time.Sleep(80 * time.Millisecond) // a press takes a moment, and each lands in its own frame
		}
	}
}

// scriptNews takes the script's next steps, on the loop.
func (a *App) scriptNews() {
	for {
		select {
		case st := <-a.script:
			switch {
			case st.quit:
				a.quit = true
				a.Quit() // and wake the loop, which may be waiting
			case st.shot != "":
				// The frame is drawn before any later step is taken.
				a.shot, a.dirty = st.shot, true
				return
			default:
				a.dispatch(input.Event{Button: st.button, Kind: input.Press})
				a.dispatch(input.Event{Button: st.button, Kind: input.Release})
				a.dirty = true
			}
		default:
			return
		}
	}
}

// saveShot writes what the renderer has drawn to a.shot, before it is shown.
func (a *App) saveShot() {
	path := a.shot
	a.shot = ""
	defer func() {
		if len(a.script) > 0 {
			a.wake()
		}
	}()
	w, h, err := a.renderer.GetOutputSize()
	if err != nil {
		a.opts.Log.Warn("screenshot", "err", err)
		return
	}
	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	if err := a.renderer.ReadPixels(nil, sdl.PIXELFORMAT_ABGR8888, unsafe.Pointer(&img.Pix[0]), img.Stride); err != nil {
		a.opts.Log.Warn("screenshot", "err", err)
		return
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	f, err := os.Create(path)
	if err != nil {
		a.opts.Log.Warn("screenshot", "err", err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		a.opts.Log.Warn("screenshot", "err", err)
		return
	}
	a.opts.Log.Info("screenshot", "path", path)
}
