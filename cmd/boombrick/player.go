package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ujjwal313/boombrick/internal/audio"
	"github.com/ujjwal313/boombrick/internal/audio/alsa"
	"github.com/ujjwal313/boombrick/internal/audio/capture"
	"github.com/ujjwal313/boombrick/internal/audio/decode"
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/peq"
	"github.com/ujjwal313/boombrick/internal/platform"
	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/settings"
	"github.com/ujjwal313/boombrick/internal/spotify"
)

// newPlayer makes the player both of Boombrick's modes play with: files
// through ALSA, and Spotify songs from a downloaded copy when Prefer
// downloaded copies is on and there is one, or else streamed through the
// helper. notify hears the player's news about streaming.
func newPlayer(db *sql.DB, prefs *settings.Settings, sp *spotify.Helper, online func() bool, log *slog.Logger,
	notify func(player.Notice)) *player.Player {
	p := player.New(output(log), decode.Open, log)
	p.SetLevelling(prefs.Bool(settings.VolumeLevelling))
	if sp == nil {
		return p // the offline build: Spotify songs aren't played
	}
	p.SetStreaming(func(ctx context.Context, t player.Track, frame int64, level bool) (audio.Source, error) {
		// A song an import downloaded plays from its file: no key, no Wi-Fi.
		if prefs.Bool(settings.PreferDownloaded) {
			if path, ok, err := library.FileOf(db, t.SpotifyID); err == nil && ok {
				return decode.Open(path)
			}
		}
		if !online() {
			return nil, spotify.ErrOffline
		}
		s, err := sp.Open(ctx, t.SpotifyID, frame, prefs.Int(settings.StreamingKbps), level)
		if err != nil {
			return nil, err // not s: a nil *Stream isn't a nil Source
		}
		return s, nil
	}, notify)
	return p
}

// output is where the music goes: the default ALSA device. On a desktop,
// $BOOMBRICK_CAPTURE names a WAV file that records everything sent to it,
// and with $BOOMBRICK_NO_ALSA=1 the file alone takes it, kept in real time:
// a stand-in for the sound card, for checking what the device would hear.
func output(log *slog.Logger) audio.Output {
	path := os.Getenv("BOOMBRICK_CAPTURE")
	if path == "" {
		return alsa.New()
	}
	if backgroundMode {
		path = strings.TrimSuffix(path, ".wav") + ".background.wav" // not over the foreground's
	}
	var inner audio.Output = alsa.New()
	if os.Getenv("BOOMBRICK_NO_ALSA") == "1" {
		inner = nil
	}
	c, err := capture.New(path, inner)
	if err != nil {
		log.Warn("capture", "path", path, "err", err)
		return alsa.New()
	}
	log.Info("capturing the output", "path", path, "alsa", inner != nil)
	captures = append(captures, c)
	return c
}

// backgroundMode is set while the program is the background player.
var backgroundMode bool

// captures are the recordings made, finished as the program ends.
var captures []*capture.Output

func finishCaptures() {
	for _, c := range captures {
		c.Finish()
	}
}

// openEQ opens the equaliser kept in the data folder. Presets to import are
// looked for in the EQ folder of the music, and in $BOOMBRICK_EQ_IMPORT.
// Both the foreground and the background player open it, from the same
// files, so the music sounds the same in each.
func openEQ(paths platform.Paths) (*peq.Manager, error) {
	imports := []string{filepath.Join(paths.Music, "EQ")}
	if d := os.Getenv("BOOMBRICK_EQ_IMPORT"); d != "" {
		imports = append(imports, d)
	}
	return peq.Open(filepath.Join(paths.Data, "eq"), peq.NewProcessor(), imports...)
}

// online reports whether the device has a network connection.
func online() bool { return platform.Online("/sys") }

// newHelper makes the Spotify helper both modes use: the program's, in its
// bin folder, with its sessions kept in the data folder.
func newHelper(dir string, paths platform.Paths, log *slog.Logger, out io.Writer) *spotify.Helper {
	return spotify.NewHelper(filepath.Join(dir, "bin", "boombrick-spotify"), filepath.Join(paths.Data, "spotify"), log, out)
}
