package scrobble

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ujjwal313/boombrick/internal/store"
)

func TestImportHistory(t *testing.T) {
	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "boombrick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, r := range [][]string{
		{"/m/homogenic/jóga.opus", "Björk", "Homogenic", "Jóga"},
		{"/m/live/jóga.opus", "Björk", "Live at Royal Opera House", "Jóga"},
		{"/m/vespertine/pagan.opus", "Björk", "Vespertine", "Pagan Poetry"},
	} {
		if _, err := st.DB.Exec(`INSERT INTO tracks (path, size, mtime, artist, album, title, number, added)
			VALUES (?, 1, 1, ?, ?, ?, 1, 1)`, r[0], r[1], r[2], r[3]); err != nil {
			t.Fatal(err)
		}
	}
	// One play Boombrick recorded itself, sent, and so in the history too.
	st.DB.Exec(`INSERT INTO plays (item, artist, title, started, sent) VALUES ('/m/vespertine/pagan.opus', 'Björk', 'Pagan Poetry', 1700000300, 1)`)
	tsv := "1700000000\tbjörk\tLive at Royal Opera House\tJóga\n" +
		"1700000100\tBjörk\tHomogenic\tjóga \n" +
		"1700000310\tBjörk\tVespertine\tPagan Poetry\n" + // the one already kept
		"1700000400\tSomeone Else\tElse\tNot Here\n" +
		"bad line\n"
	if err := os.WriteFile(filepath.Join(data, HistoryFile), []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}
	taken, unmatched, err := ImportHistory(st.DB, data)
	if err != nil || taken != 2 || unmatched != 1 {
		t.Fatalf("taken %d unmatched %d: %v", taken, unmatched, err)
	}
	var live, sent int
	st.DB.QueryRow(`SELECT count(*) FROM plays WHERE item = '/m/live/jóga.opus'`).Scan(&live)
	st.DB.QueryRow(`SELECT count(*) FROM plays WHERE sent = 0`).Scan(&sent)
	if live != 1 || sent != 0 {
		t.Fatalf("live %d, unsent %d", live, sent)
	}
	// The play with no file stays, for when its album arrives.
	b, _ := os.ReadFile(filepath.Join(data, HistoryFile))
	if string(b) != "1700000400\tSomeone Else\tElse\tNot Here\n" {
		t.Fatalf("left: %q", b)
	}
	if taken, unmatched, err := ImportHistory(st.DB, data); err != nil || taken != 0 || unmatched != 1 {
		t.Fatalf("again: %d %d %v", taken, unmatched, err)
	}
	// Its album synced and scanned: taken, and the file done with.
	st.DB.Exec(`INSERT INTO tracks (path, size, mtime, artist, album, title, number, added) VALUES ('/m/else.opus', 1, 1, 'Someone Else', 'Else', 'Not Here', 1, 1)`)
	if taken, unmatched, err := ImportHistory(st.DB, data); err != nil || taken != 1 || unmatched != 0 {
		t.Fatalf("after the sync: %d %d %v", taken, unmatched, err)
	}
	if _, err := os.Stat(filepath.Join(data, HistoryFile+".imported")); err != nil {
		t.Fatal("not marked as taken")
	}
}
