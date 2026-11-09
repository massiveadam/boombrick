package scrobble

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/store"
)

func TestSignMatchesLastFM(t *testing.T) {
	p := url.Values{"method": {"track.scrobble"}, "api_key": {"k"}, "sk": {"s"}, "artist[0]": {"Björk"},
		"track[0]": {"Jóga"}, "timestamp[0]": {"1700000000"}, "format": {"json"}}
	// The same parameters signed by an independent Python implementation.
	if got := Sign(p, "secret"); got != "2950db64912337332e2e1138769e951b" {
		t.Fatalf("sig %s", got)
	}
}

type fakePlayer struct {
	mu sync.Mutex
	st player.State
}

func (f *fakePlayer) State() player.State { f.mu.Lock(); defer f.mu.Unlock(); return f.st }
func (f *fakePlayer) set(st player.State) { f.mu.Lock(); f.st = st; f.mu.Unlock() }

type lastfm struct {
	mu    sync.Mutex
	calls []url.Values
	fail  int // error code to answer with, 0 for success
}

func (l *lastfm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	v, _ := url.ParseQuery(string(b))
	l.mu.Lock()
	l.calls = append(l.calls, v)
	fail := l.fail
	l.mu.Unlock()
	if fail != 0 {
		io.WriteString(w, `{"error":`+itoa(fail)+`,"message":"no"}`)
		return
	}
	io.WriteString(w, `{"scrobbles":{"@attr":{"accepted":1,"ignored":0}}}`)
}

func itoa(n int) string { return strconv.Itoa(n) }

func (l *lastfm) methods(m string) []url.Values {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []url.Values
	for _, c := range l.calls {
		if c.Get("method") == m {
			out = append(out, c)
		}
	}
	return out
}

func rig(t *testing.T, srv *lastfm) (*Scrobbler, *store.Store, string) {
	t.Helper()
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "lastfm.json"), []byte(`{"api_key":"k","api_secret":"secret","session_key":"s","user":"adam"}`), 0o600)
	st, err := store.Open(filepath.Join(data, "boombrick.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(st.DB, data, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s == nil {
		t.Fatal("no scrobbler with credentials")
	}
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	s.Endpoint, s.Look, s.FirstSend, s.SendEvery = hs.URL, 2*time.Millisecond, time.Hour, time.Hour
	return s, st, data
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(3 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func count(db *store.Store, where string) int {
	var n int
	db.DB.QueryRow(`SELECT count(*) FROM scrobbles WHERE ` + where).Scan(&n)
	return n
}

var song = player.Track{Path: "/m/a.opus", Artist: "Björk", Title: "Jóga", Album: "Homogenic", Duration: 5 * time.Minute}

func TestAPlayCountsAtHalfAndIsSent(t *testing.T) {
	srv := &lastfm{}
	s, db, _ := rig(t, srv)
	p := &fakePlayer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, p)

	p.set(player.State{Queued: true, Track: song, Playing: true, Elapsed: 10 * time.Second, Duration: song.Duration})
	waitFor(t, "now playing", func() bool { return len(srv.methods("track.updateNowPlaying")) == 1 })
	time.Sleep(20 * time.Millisecond)
	if count(db, "1") != 0 {
		t.Fatal("recorded before half")
	}
	p.set(player.State{Queued: true, Track: song, Playing: true, Elapsed: 151 * time.Second, Duration: song.Duration})
	waitFor(t, "the play sent", func() bool { return count(db, "sent = 1") == 1 })
	c := srv.methods("track.scrobble")[0]
	if c.Get("artist[0]") != "Björk" || c.Get("track[0]") != "Jóga" || c.Get("album[0]") != "Homogenic" || c.Get("sk") != "s" ||
		c.Get("api_sig") != Sign(c, "secret") {
		t.Fatalf("scrobble %v", c)
	}
	if ts := c.Get("timestamp[0]"); ts == "" {
		t.Fatal("no timestamp")
	}
	// The background player, handed this play, sees it too, a few seconds
	// apart by its own reckoning: not again.
	var started int64
	db.DB.QueryRow(`SELECT started FROM scrobbles`).Scan(&started)
	if err := s.record(item(song), song, song.Duration, time.Unix(started+3, 0)); err != nil || count(db, "1") != 1 {
		t.Fatalf("recorded twice: %v %d", err, count(db, "1"))
	}
}

func TestShortTracksAndPausesDontCount(t *testing.T) {
	srv := &lastfm{}
	s, db, _ := rig(t, srv)
	p := &fakePlayer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, p)
	short := player.Track{Path: "/m/b.opus", Artist: "A", Title: "Intro", Duration: 25 * time.Second}
	p.set(player.State{Queued: true, Track: short, Playing: true, Elapsed: 24 * time.Second, Duration: short.Duration})
	time.Sleep(30 * time.Millisecond)
	paused := song
	p.set(player.State{Queued: true, Track: paused, Playing: false, Elapsed: 4 * time.Minute, Duration: song.Duration})
	time.Sleep(30 * time.Millisecond)
	if count(db, "1") != 0 {
		t.Fatal("a short track or a paused one was recorded")
	}
}

func TestLongTrackCountsAtFourMinutes(t *testing.T) {
	srv := &lastfm{}
	s, db, _ := rig(t, srv)
	p := &fakePlayer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, p)
	long := player.Track{Path: "/m/c.opus", Artist: "A", Title: "Long", Duration: 20 * time.Minute}
	p.set(player.State{Queued: true, Track: long, Playing: true, Elapsed: 4*time.Minute + time.Second, Duration: long.Duration})
	waitFor(t, "recorded at 4 minutes", func() bool { return count(db, "1") == 1 })
}

func TestOfflinePlaysWaitAndARefusedSessionStops(t *testing.T) {
	srv := &lastfm{fail: 11} // service offline
	s, db, _ := rig(t, srv)
	if err := s.record(item(song), song, song.Duration, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SendWaiting(context.Background()); err == nil || count(db, "sent = 0") != 1 {
		t.Fatalf("offline: %v, waiting %d", err, count(db, "sent = 0"))
	}
	srv.mu.Lock()
	srv.fail = 9 // invalid session
	srv.mu.Unlock()
	err := s.SendWaiting(context.Background())
	var fatal fatalErr
	if !errors.As(err, &fatal) || count(db, "sent = 0") != 1 {
		t.Fatalf("refused: %v", err)
	}
	srv.mu.Lock()
	srv.fail = 0
	srv.mu.Unlock()
	if err := s.SendWaiting(context.Background()); err != nil || count(db, "sent = 1") != 1 {
		t.Fatalf("back online: %v", err)
	}
}

func TestNoCredentialsNoScrobbler(t *testing.T) {
	if New(nil, t.TempDir(), slog.Default()) != nil {
		t.Fatal("a scrobbler without lastfm.json")
	}
}
