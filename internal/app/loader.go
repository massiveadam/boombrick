package app

import (
	"time"

	"github.com/ujjwal313/boombrick/internal/history"
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/resume"
)

// ScanUpdate is what the app hears from the scanner: that a scan wrote to the
// library, that it ended, or both.
type ScanUpdate struct {
	Changed bool // tracks were added, changed or removed
	Ended   bool // the scan finished
}

// loaded is a read's result. A nil index means only progress was read.
type loaded struct {
	ix       *library.Index
	progress []resume.Progress
	history  *history.History // nil without a history to read
	final    bool             // a scan had ended when the read began
	took     time.Duration
	err      error
}

// loader reads the library index and the albums in progress on a goroutine,
// one read at a time. Asking while a read runs queues one more, which reads
// the index too if any of the asks wanted it. It is used from the UI thread.
type loader struct {
	index    func() (*library.Index, error)
	progress func() ([]resume.Progress, error)
	history  func() (*history.History, error) // read with the progress; nil for none
	wake     func()
	results  chan loaded

	running bool
	again   bool // another read was asked for while one ran
	againIx bool // and it needs the index
}

func newLoader(index func() (*library.Index, error), progress func() ([]resume.Progress, error), wake func()) *loader {
	return &loader{index: index, progress: progress, wake: wake, results: make(chan loaded, 1)}
}

// start reads the progress, and the index too when index is set, unless a
// read is running, in which case one more is queued. final says whether a
// scan had ended when it was asked for; a queued read takes the final given
// to take when it begins.
func (l *loader) start(index, final bool) {
	if l.running {
		l.again, l.againIx = true, l.againIx || index
		return
	}
	l.running = true
	go func() {
		begin := time.Now()
		r := loaded{final: final}
		if index {
			r.ix, r.err = l.index()
		}
		if r.err == nil {
			r.progress, r.err = l.progress()
		}
		if r.err == nil && l.history != nil {
			r.history, r.err = l.history()
		}
		r.took = time.Since(begin)
		l.results <- r
		l.wake()
	}()
}

// take returns a finished read, if there is one, and starts the read queued
// behind it, which records final.
func (l *loader) take(final bool) (loaded, bool) {
	select {
	case r := <-l.results:
		l.running = false
		if l.again {
			index := l.againIx
			l.again, l.againIx = false, false
			l.start(index, final)
		}
		return r, true
	default:
		return loaded{}, false
	}
}

// reloadAfter decides what to read after scan updates and saves: the index
// when a scan wrote to the library, or when a scan ended with nothing shown
// yet (the first scan of an empty card); only the progress after a save.
func reloadAfter(ended, changed, saved, shown bool) (read, index bool) {
	switch {
	case changed || ended && !shown:
		return true, true
	case saved:
		return true, false
	}
	return false, false
}
