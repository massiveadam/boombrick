package voice

import (
	"context"
	"log/slog"
)

// Buttons are the key codes push-to-talk answers to; 0 leaves one unused.
type Buttons struct {
	Talk  uint16 // held: record; released: send
	Enter uint16 // pressed: Enter, after reading the typed text
	Clear uint16 // pressed: clear the typed line
}

// clip is what PTT needs of a Recorder.
type clip interface {
	Start() error
	Stop() ([]byte, error)
}

// remote is what PTT needs of a Client.
type remote interface {
	Dictate(ctx context.Context, wav []byte) (string, error)
	Key(ctx context.Context, name string) error
}

// PTT runs push-to-talk on the key events from events until ctx ends or
// events closes. Clips are sent one at a time; a press while one is being
// sent waits for it. say is told what happens, for the screen or a log.
func PTT(ctx context.Context, events <-chan Event, b Buttons, rec clip, c remote, say func(string), log *slog.Logger) {
	if say == nil {
		say = func(string) {}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	recording := false
	for {
		var ev Event
		select {
		case <-ctx.Done():
			if recording {
				rec.Stop()
			}
			return
		case e, ok := <-events:
			if !ok {
				if recording {
					rec.Stop()
				}
				return
			}
			ev = e
		}
		switch {
		case b.Talk != 0 && ev.Code == b.Talk && ev.Value == 1 && !recording:
			if err := rec.Start(); err != nil {
				log.Warn("ptt record", "err", err)
				say("can't record: " + err.Error())
				continue
			}
			recording = true
			say("listening…")
		case b.Talk != 0 && ev.Code == b.Talk && ev.Value == 0 && recording:
			recording = false
			wav, err := rec.Stop()
			switch {
			case err != nil:
				log.Warn("ptt record", "err", err)
				say("recording failed: " + err.Error())
			case wav == nil:
				say("") // a tap: nothing to send
			default:
				say("transcribing…")
				text, err := c.Dictate(ctx, wav)
				switch {
				case err != nil:
					log.Warn("ptt dictate", "err", err)
					say("not sent: " + err.Error())
				case text == "":
					say("heard nothing")
				default:
					say("typed: " + text)
				}
			}
		case b.Enter != 0 && ev.Code == b.Enter && ev.Value == 1 && !recording:
			if err := c.Key(ctx, "enter"); err != nil {
				say("enter failed: " + err.Error())
			} else {
				say("sent")
			}
		case b.Clear != 0 && ev.Code == b.Clear && ev.Value == 1 && !recording:
			if err := c.Key(ctx, "clear"); err != nil {
				say("clear failed: " + err.Error())
			} else {
				say("cleared")
			}
		}
	}
}
