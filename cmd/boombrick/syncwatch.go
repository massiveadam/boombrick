package main

import (
	"context"
	"os"
	"time"
)

// rescanAfterSync rescans the library when the RG SP's music sync has
// changed it, so what the sync brings or removes shows without a rescan by
// hand. The sync saves its state file as it goes; looked at every interval,
// a change rescans once the file has been still for an interval (a run done)
// or, during a long run, at most every syncRescanEvery.
func rescanAfterSync(ctx context.Context, state string, interval time.Duration, rescan func()) {
	seen := mtime(state) // the scan at start covers what is there now
	last := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m := mtime(state)
		if m.Equal(seen) {
			continue
		}
		if time.Since(m) < interval && time.Since(last) < syncRescanEvery {
			continue // still being written: wait for the run, or for the next while it lasts
		}
		seen, last = m, time.Now()
		rescan()
	}
}

const syncRescanEvery = 10 * time.Minute

func mtime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
