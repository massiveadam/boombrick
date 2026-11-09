package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/store"
)

func TestLoad(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "boombrick.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.Local)
	lastYear := time.Date(2025, 11, 9, 20, 0, 0, 0, time.Local)
	for _, p := range []struct {
		item string
		at   time.Time
	}{
		{"/a.opus", now.Add(-time.Hour)}, {"/a.opus", now.Add(-48 * time.Hour)},
		{"/b.opus", lastYear},
		{"/c.opus", now.AddDate(0, 0, -1)}, // today's date this year only counts as "this day" in years before
	} {
		if _, err := st.DB.Exec(`INSERT INTO plays (item, artist, title, started) VALUES (?, 'A', 'T', ?)`, p.item, p.at.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Load(st.DB, now)
	if err != nil {
		t.Fatal(err)
	}
	if a := h.Tracks["/a.opus"]; a.Plays != 2 || !a.Last.Equal(now.Add(-time.Hour).Truncate(time.Second)) {
		t.Fatalf("a: %+v", a)
	}
	if !h.ThisDay["/b.opus"] || h.ThisDay["/a.opus"] || h.ThisDay["/c.opus"] || len(h.ThisDay) != 1 {
		t.Fatalf("this day: %v", h.ThisDay)
	}
}
