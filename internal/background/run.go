package background

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/resume"
)

const (
	// fadeOut is how long the music fades as Boombrick takes it back.
	fadeOut = 300 * time.Millisecond
	// quiet is how long nothing has played before playback counts as
	// stopped, so the moment between two tracks doesn't. It counts from the
	// first look that heard nothing, which may come just as the music
	// stopped.
	quiet = time.Second
	// readyEvery is how often, at most, the player is looked at until the
	// music plays, as Boombrick waits to hear that it does before quitting.
	readyEvery = 250 * time.Millisecond
)

// Player is what Run plays with. *player.Player is one.
type Player interface {
	State() player.State
	Toggle()
	Next()
	FadeOut(d time.Duration)
	ResumeSleep(s player.Sleep)
	Sleep() player.Sleep
	Snapshot() (player.Snapshot, bool)
}

var _ Player = (*player.Player)(nil)

// Options are what Run works with besides its player.
type Options struct {
	Player Player
	// Save stores the queue's place, and Handover what Boombrick needs with
	// it to carry on.
	Save     func(player.Snapshot) error
	Handover func(resume.Handover) error
	Sleep    player.Sleep // the sleep timer handed over; resumed once the music plays
	// StayAwake is the firmware's keep-awake file, written again while the
	// music plays; "" where there is none.
	StayAwake string
	Ready     func() // called once the music really plays
	Log       *slog.Logger
	// Every is how often the player is looked at once the music plays, and
	// SaveEvery how often the queue's place is saved; tests shorten them.
	Every, SaveEvery time.Duration
	// StallLimit is how long a stream may load or stay stalled before the music
	// counts as stopped, as when the Wi-Fi is gone; tests shorten it.
	StallLimit time.Duration
	// SoundUsers lists the programs that have the sound card open, for the
	// log when it fails; nil lists none.
	SoundUsers func() []string
	// Held reports whether the music was paused on purpose, as from the
	// RG SP's volume keys, and is to wait to be played again rather than count
	// as stopped; nil is never.
	Held func() bool
}

// Run plays the queue its player has restored until ctx ends, as Boombrick
// takes the music back, or playback stops by itself: the end of the queue,
// the sleep timer, a stream loading or stalled for StallLimit, every song
// skipped. Either way it saves the queue's place and, for Boombrick, whether
// the music was playing: taken back it was, stopped by itself it wasn't. A song
// still waiting for its download is skipped, since downloads wait for
// Boombrick, unless it is stopped on, as by the sleep timer. Once the music
// really plays Run says so and resumes the sleep timer handed over. The
// keep-awake file is removed when the music stops by itself, but only once it
// has played: a player that stops first leaves it to Boombrick.
func Run(ctx context.Context, o Options) error {
	p := o.Player
	p.Toggle()
	every := min(o.Every, readyEvery)
	tick := time.NewTicker(every)
	defer tick.Stop()
	var (
		ready  bool
		hushed bool          // the last look heard nothing
		silent time.Duration // how long nothing has played
		stuck  time.Duration // how long a stream meant to play has loaded or stalled without a sound
		skips  int           // songs skipped in a row
		saved  time.Time
	)
	for {
		select {
		case <-ctx.Done():
			return o.takenBack(ready, !hushed)
		case <-tick.C:
		}
		st := p.State()
		switch {
		case st.Waiting && !st.Cued:
			if st.Index+1 >= st.Count && st.Repeat != player.RepeatAll {
				return o.stopped(ready) // the last song, and Next does nothing there
			}
			if skips >= st.Count {
				return o.stopped(ready) // every song is waiting for its download
			}
			skips++
			o.Log.Info("skipped a song not downloaded yet", "title", st.Track.Title)
			p.Next()
		case st.Playing && (st.Stalled || st.Loading):
			hushed, silent, skips = false, 0, 0
			if stuck += every; o.StallLimit > 0 && stuck >= o.StallLimit {
				return o.stopped(ready)
			}
		case st.Playing && !st.Waiting:
			hushed, silent, stuck, skips = false, 0, 0, 0
			if !ready {
				ready = true
				if o.Sleep.Mode != player.SleepOff {
					p.ResumeSleep(o.Sleep)
				}
				o.Ready()
				every = o.Every
				tick.Reset(every)
			}
		case o.Held != nil && o.Held():
			hushed, silent = false, 0
		default:
			if hushed {
				silent += every
			}
			if hushed = true; silent >= quiet {
				if st.Err != nil {
					o.soundFailed(st.Err)
				}
				return o.stopped(ready)
			}
		}
		if time.Since(saved) >= o.SaveEvery {
			saved = time.Now()
			o.save()
			o.awake()
		}
	}
}

// takenBack fades the music out and saves its place, with the sleep timer, for
// Boombrick to carry on from; playing if the last look heard music, so that
// music which just stopped by itself, as at the sleep timer's fade, isn't
// started again. Until Run has resumed the timer the player has none, and the
// one handed over is handed back.
func (o Options) takenBack(resumed, heard bool) error {
	o.Player.FadeOut(fadeOut)
	for end := time.Now().Add(fadeOut + time.Second); o.fading() && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
	o.save()
	sleep := o.Sleep
	if resumed {
		sleep = o.Player.Sleep()
	}
	return o.Handover(resume.Handover{Playing: heard, Sleep: sleep})
}

// stopped saves the queue's place once playback has stopped by itself, and
// lets the Brick sleep again. A player that never played leaves the keep-awake
// file alone: Boombrick, still playing, owns it.
func (o Options) stopped(played bool) error {
	o.save()
	if o.StayAwake != "" && played {
		os.Remove(o.StayAwake)
	}
	return o.Handover(resume.Handover{})
}

// soundFailed logs the sound card's error, with what has the card open: on
// the Brick it is often another program holding it.
func (o Options) soundFailed(err error) {
	var users []string
	if o.SoundUsers != nil {
		users = o.SoundUsers()
	}
	o.Log.Warn("the sound card failed", "err", err, "open_by", users)
}

func (o Options) save() {
	if s, ok := o.Player.Snapshot(); ok {
		if err := o.Save(s); err != nil {
			o.Log.Warn("save queue", "err", err)
		}
	}
}

// awake keeps the Brick from sleeping under the music: the launcher removes
// the file when Boombrick's screen side quits.
func (o Options) awake() {
	if o.StayAwake == "" {
		return
	}
	if err := os.WriteFile(o.StayAwake, []byte("1\n"), 0o644); err != nil {
		o.Log.Warn("keep the Brick awake", "err", err)
	}
}

// fading reports whether there is music still to fade: a stream being opened
// has none.
func (o Options) fading() bool {
	st := o.Player.State()
	return st.Playing && !st.Loading
}
