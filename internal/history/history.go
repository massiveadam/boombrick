// Package history reads Boombrick's listening history (the plays table the
// scrobble package keeps) for Home's blocks: how often and how lately each
// file was played, and what was played on this day in other years. It is two
// grouped queries, read with the library off the UI thread.
package history

import (
	"database/sql"
	"time"
)

// Track is a file's plays.
type Track struct {
	Plays int
	Last  time.Time
}

// History is the history as read at a moment.
type History struct {
	Tracks  map[string]Track // by path
	ThisDay map[string]bool  // the paths played on this day of the year, in years before
}

// Load reads the history as at now, whose month and day make "this day".
func Load(db *sql.DB, now time.Time) (*History, error) {
	h := &History{Tracks: map[string]Track{}, ThisDay: map[string]bool{}}
	rows, err := db.Query(`SELECT item, count(*), max(started) FROM plays GROUP BY item`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var path string
		var n int
		var last int64
		if err := rows.Scan(&path, &n, &last); err != nil {
			rows.Close()
			return nil, err
		}
		h.Tracks[path] = Track{Plays: n, Last: time.Unix(last, 0)}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()).Unix()
	rows, err = db.Query(`SELECT DISTINCT item FROM plays
		WHERE strftime('%m-%d', started, 'unixepoch', 'localtime') = ? AND started < ?`, now.Format("01-02"), yearStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		h.ThisDay[path] = true
	}
	return h, rows.Err()
}
