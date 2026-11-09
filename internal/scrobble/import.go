package scrobble

import (
	"bufio"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// HistoryFile is the Last.fm history to take into the plays table, in the
// data folder: a line a play, its Unix time, artist, album and title
// separated by tabs, as fetched from user.getRecentTracks. What is taken
// leaves the file; plays with no file in the library yet stay in it, tried
// again after the next scan, as the music sync brings their albums. Once all
// are taken it is renamed with .imported added.
const HistoryFile = "lastfm-history.tsv"

// ImportHistory takes the history file in data into the plays, matched to the
// library's files by artist and title (and album where it tells two apart),
// as already sent, and leaves in the file only the plays that found no file.
// A play already kept, as one Boombrick itself recorded and sent, isn't taken
// twice. It reports how many were taken and how many found no file; with no
// history file it does nothing.
func ImportHistory(db *sql.DB, data string) (taken, unmatched int, err error) {
	path := filepath.Join(data, HistoryFile)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	type file struct{ path, album, albumArtist string }
	byTrack := map[string][]file{} // artist and title, folded
	rows, err := db.Query(`SELECT path, artist, album_artist, album, title FROM tracks`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var p, artist, albumArtist, album, title string
		if err := rows.Scan(&p, &artist, &albumArtist, &album, &title); err != nil {
			rows.Close()
			return 0, 0, err
		}
		for _, a := range []string{artist, albumArtist} {
			if a != "" {
				k := fold(a) + "\x00" + fold(title)
				byTrack[k] = append(byTrack[k], file{p, album, albumArtist})
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	seen, err := tx.Prepare(`SELECT count(*) FROM plays WHERE item = ? AND abs(started - ?) < ?`)
	if err != nil {
		return 0, 0, err
	}
	add, err := tx.Prepare(`INSERT INTO plays (item, artist, title, album, album_artist, started, sent) VALUES (?, ?, ?, ?, ?, ?, 1)`)
	if err != nil {
		return 0, 0, err
	}
	var left []string // the plays with no file yet, kept for the next try
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 4 {
			continue
		}
		ts, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || ts <= 0 {
			continue
		}
		artist, album, title := parts[1], parts[2], parts[3]
		files := byTrack[fold(artist)+"\x00"+fold(title)]
		if len(files) == 0 {
			unmatched++
			left = append(left, sc.Text())
			continue
		}
		match := files[0]
		for _, c := range files {
			if fold(c.album) == fold(album) {
				match = c
				break
			}
		}
		var n int
		if err := seen.QueryRow(match.path, ts, sameStart).Scan(&n); err != nil {
			return 0, 0, err
		}
		if n > 0 {
			continue
		}
		if _, err := add.Exec(match.path, artist, title, match.album, match.albumArtist, ts); err != nil {
			return 0, 0, err
		}
		taken++
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	f.Close()
	if len(left) == 0 {
		return taken, unmatched, os.Rename(path, path+".imported")
	}
	if taken == 0 {
		return taken, unmatched, nil // as it was
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(strings.Join(left, "\n")+"\n"), 0o644); err != nil {
		return taken, unmatched, err
	}
	return taken, unmatched, os.Rename(tmp, path)
}

// fold is s for matching: lower case, trimmed.
func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
