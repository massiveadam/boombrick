// Package scrobble keeps Boombrick's listening history and sends it to
// Last.fm. It watches the player and records each play that counts by
// Last.fm's rule - a track longer than 30 seconds, heard for half its length
// or 4 minutes, whichever comes first - with the time it started, in the
// plays table, which Home's blocks read. With Last.fm set up it also sends
// what is waiting whenever Last.fm can be reached, so plays heard away from
// Wi-Fi arrive later at their own times, and shows the track playing.
//
// Last.fm is set up by lastfm.json in the data folder, holding api_key,
// api_secret and session_key (a session from Last.fm's web authorisation,
// which doesn't expire) and user.
package scrobble

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ujjwal313/boombrick/internal/player"
)

// Endpoint is Last.fm's API.
const Endpoint = "https://ws.audioscrobbler.com/2.0/"

const (
	minLength = 30 * time.Second // shorter tracks aren't scrobbled
	enough    = 4 * time.Minute  // heard this long, a track counts whatever its length
	batch     = 50               // the most Last.fm takes in one request
	sameStart = 60               // seconds: two records of one play differ by less
)

// Creds are what lastfm.json holds.
type Creds struct {
	APIKey     string `json:"api_key"`
	APISecret  string `json:"api_secret"`
	SessionKey string `json:"session_key"`
	User       string `json:"user"`
}

// LoadCreds reads the credentials in the data folder; false without them.
func LoadCreds(data string) (Creds, bool) {
	b, err := os.ReadFile(filepath.Join(data, "lastfm.json"))
	if err != nil {
		return Creds{}, false
	}
	var c Creds
	if json.Unmarshal(b, &c) != nil || c.APIKey == "" || c.APISecret == "" || c.SessionKey == "" {
		return Creds{}, false
	}
	return c, true
}

// Player is what is watched. *player.Player is one.
type Player interface {
	State() player.State
}

// Scrobbler records plays and, with Last.fm set up, sends them.
type Scrobbler struct {
	db       *sql.DB
	creds    Creds
	online   bool // Last.fm is set up
	log      *slog.Logger
	lock     string // held while sending, so the app and its background player don't both send
	Endpoint string
	Client   *http.Client
	now      func() time.Time
	// Tunables, which tests shorten.
	Look, SendEvery, FirstSend time.Duration
	wake                       chan struct{}
}

// New returns a scrobbler for the data folder: it records plays, and sends
// them only with Last.fm's credentials there.
func New(db *sql.DB, data string, log *slog.Logger) *Scrobbler {
	c, ok := LoadCreds(data)
	return &Scrobbler{db: db, creds: c, online: ok, log: log.With("from", "lastfm"), lock: filepath.Join(data, "lastfm.lock"),
		Endpoint: Endpoint, Client: &http.Client{Timeout: 20 * time.Second}, now: time.Now,
		Look: time.Second, SendEvery: time.Minute, FirstSend: 10 * time.Second, wake: make(chan struct{}, 1)}
}

// Run watches p, and sends with Last.fm set up, until ctx ends.
func (s *Scrobbler) Run(ctx context.Context, p Player) {
	if s.online {
		s.log.Info("scrobbling", "user", s.creds.User)
		go s.sendLoop(ctx)
	}
	s.watch(ctx, p)
}

// item names a track's audio, for telling one play from the next.
func item(t player.Track) string {
	switch {
	case t.Path != "":
		return t.Path
	case t.SpotifyID != "":
		return "spotify:" + t.SpotifyID
	case t.VideoID != "":
		return "youtube:" + t.VideoID
	}
	return ""
}

func (s *Scrobbler) watch(ctx context.Context, p Player) {
	tick := time.NewTicker(s.Look)
	defer tick.Stop()
	var (
		cur      string
		started  time.Time
		last     time.Duration
		recorded bool
		shown    bool
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		st := p.State()
		it := item(st.Track)
		if it == "" {
			continue
		}
		now := s.now()
		// A new track, or this one started again (repeat, or Prev to its
		// start): a new play.
		if it != cur || st.Elapsed+5*time.Second < last {
			cur, started, recorded, shown = it, now.Add(-st.Elapsed), false, false
		}
		last = st.Elapsed
		if !st.Playing {
			continue
		}
		dur := st.Duration
		if dur == 0 {
			dur = st.Track.Duration
		}
		if !shown {
			shown = true
			if s.online {
				go s.nowPlaying(ctx, st.Track, dur)
			}
		}
		if !recorded && dur > minLength && st.Elapsed >= min(dur/2, enough) {
			recorded = true
			if err := s.record(it, st.Track, dur, started); err != nil {
				s.log.Warn("record a play", "err", err)
			}
		}
	}
}

// record keeps the play for sending, unless this play is kept already: the
// app and the background player each see a play handed between them.
func (s *Scrobbler) record(it string, t player.Track, dur time.Duration, started time.Time) error {
	if t.Artist == "" || t.Title == "" {
		return nil // Last.fm wants both
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM plays WHERE item = ? AND abs(started - ?) < ?`,
		it, started.Unix(), sameStart).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := s.db.Exec(`INSERT INTO plays (item, artist, title, album, album_artist, duration, started)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, it, t.Artist, t.Title, t.Album, t.AlbumArtist, int(dur.Seconds()), started.Unix()); err != nil {
		return err
	}
	s.log.Info("play recorded", "artist", t.Artist, "title", t.Title)
	if !s.online {
		return nil
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

func (s *Scrobbler) sendLoop(ctx context.Context) {
	t := time.NewTimer(s.FirstSend)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
		wait := s.SendEvery
		if err := s.SendWaiting(ctx); err != nil {
			var fatal fatalErr
			if errors.As(err, &fatal) {
				s.log.Warn("Last.fm refused the session: plays are kept, and not sent until it is set up again", "err", err)
				return
			}
			s.log.Debug("send", "err", err) // offline, mostly: tried again later
			wait = 5 * time.Minute
		}
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(wait)
	}
}

// SendWaiting sends the plays not yet sent, in batches, and forgets those
// sent long ago. Another process sending meanwhile is left to it.
func (s *Scrobbler) SendWaiting(ctx context.Context) error {
	f, err := os.OpenFile(s.lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	for {
		rows, err := s.waiting()
		if err != nil || len(rows) == 0 {
			return err
		}
		if err := s.send(ctx, rows); err != nil {
			return err
		}
		ids := make([]string, len(rows))
		for i, r := range rows {
			ids[i] = strconv.FormatInt(r.id, 10)
		}
		if _, err := s.db.Exec(`UPDATE plays SET sent = 1 WHERE id IN (` + strings.Join(ids, ",") + `)`); err != nil {
			return err
		}
		s.log.Info("scrobbled", "plays", len(rows))
	}
}

type row struct {
	id                                int64
	artist, title, album, albumArtist string
	duration                          int
	started                           int64
}

func (s *Scrobbler) waiting() ([]row, error) {
	q, err := s.db.Query(`SELECT id, artist, title, album, album_artist, duration, started FROM plays
		WHERE sent = 0 ORDER BY started LIMIT ?`, batch)
	if err != nil {
		return nil, err
	}
	defer q.Close()
	var out []row
	for q.Next() {
		var r row
		if err := q.Scan(&r.id, &r.artist, &r.title, &r.album, &r.albumArtist, &r.duration, &r.started); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, q.Err()
}

func (s *Scrobbler) send(ctx context.Context, rows []row) error {
	p := url.Values{}
	for i, r := range rows {
		k := func(name string) string { return fmt.Sprintf("%s[%d]", name, i) }
		p.Set(k("artist"), r.artist)
		p.Set(k("track"), r.title)
		p.Set(k("timestamp"), strconv.FormatInt(r.started, 10))
		if r.album != "" {
			p.Set(k("album"), r.album)
		}
		if r.albumArtist != "" && r.albumArtist != r.artist {
			p.Set(k("albumArtist"), r.albumArtist)
		}
		if r.duration > 0 {
			p.Set(k("duration"), strconv.Itoa(r.duration))
		}
	}
	return s.call(ctx, "track.scrobble", p)
}

func (s *Scrobbler) nowPlaying(ctx context.Context, t player.Track, dur time.Duration) {
	if t.Artist == "" || t.Title == "" {
		return
	}
	p := url.Values{"artist": {t.Artist}, "track": {t.Title}}
	if t.Album != "" {
		p.Set("album", t.Album)
	}
	if dur > 0 {
		p.Set("duration", strconv.Itoa(int(dur.Seconds())))
	}
	_ = s.call(ctx, "track.updateNowPlaying", p) // shown or not, nothing waits on it
}

// fatalErr is Last.fm refusing the key or the session: no use trying again.
type fatalErr struct{ error }

// call makes a signed POST to the API.
func (s *Scrobbler) call(ctx context.Context, method string, p url.Values) error {
	p.Set("method", method)
	p.Set("api_key", s.creds.APIKey)
	p.Set("sk", s.creds.SessionKey)
	p.Set("api_sig", Sign(p, s.creds.APISecret))
	p.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, strings.NewReader(p.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Boombrick")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var e struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	switch e.Error {
	case 0:
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("lastfm: %s: HTTP %d", method, resp.StatusCode)
		}
		return nil
	case 9, 10, 14, 26: // invalid session or key, not authorised, suspended key
		return fatalErr{fmt.Errorf("lastfm: %s: %d %s", method, e.Error, e.Message)}
	}
	return fmt.Errorf("lastfm: %s: %d %s", method, e.Error, e.Message)
}

// Sign is Last.fm's api_sig: the MD5 of every parameter but format and
// callback, name then value, sorted by name, then the secret.
func Sign(p url.Values, secret string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		if k != "format" && k != "callback" && k != "api_sig" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(p.Get(k))
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
