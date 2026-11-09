package musicsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// library makes a server library under root: path -> contents.
func library(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot is every file under root with its hash and mode, to prove root
// wasn't changed.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		info, _ := d.Info()
		key, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[key] = fmt.Sprintf("dir %v", info.Mode())
			return nil
		}
		b, _ := os.ReadFile(p)
		out[key] = fmt.Sprintf("%x %v %d", sha256.Sum256(b), info.Mode(), info.ModTime().UnixNano())
		return nil
	})
	return out
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// server is a read-only library with its manifest written outside it.
type server struct {
	root, manifest string
}

func newServer(t *testing.T, files map[string]string) server {
	t.Helper()
	base := t.TempDir()
	s := server{root: filepath.Join(base, "music"), manifest: filepath.Join(base, "meta", "manifest.json")}
	library(t, s.root, files)
	s.rebuild(t)
	return s
}

func (s server) rebuild(t *testing.T) {
	t.Helper()
	m, err := BuildManifest(s.root, BuildOptions{Cache: filepath.Join(filepath.Dir(s.manifest), "hashes.json")})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if err := writeAtomic(s.manifest, b); err != nil {
		t.Fatal(err)
	}
}

func (s server) source() DirSource { return DirSource{Root: s.root, ManifestPath: s.manifest} }

func files(t *testing.T, dest string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() && d.Name() == stateDir {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dest, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

var lib = map[string]string{
	"Artist/Album/01 One.flac": strings.Repeat("one", 5000),
	"Artist/Album/02 Two.flac": strings.Repeat("two", 7000),
	"Artist/Album/cover.jpg":   "jpeg",
	"Artist/Album/notes.txt":   "not music: left out",
	"Other/Single/a.opus":      "opus",
	".hidden/x.flac":           "hidden",
}

func TestManifestListsMusicOnly(t *testing.T) {
	s := newServer(t, lib)
	m, _ := s.source().Manifest(context.Background())
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range m.Files {
		got = append(got, e.Path)
	}
	want := []string{"Artist/Album/01 One.flac", "Artist/Album/02 Two.flac", "Artist/Album/cover.jpg", "Other/Single/a.opus"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("manifest %v", got)
	}
}

func TestManifestRefusesCacheInsideLibrary(t *testing.T) {
	root := t.TempDir()
	if _, err := BuildManifest(root, BuildOptions{Cache: filepath.Join(root, "sub", "c.json")}); err == nil {
		t.Fatal("cache inside the library accepted")
	}
}

func TestPullThenIncrementalThenDelete(t *testing.T) {
	s := newServer(t, lib)
	before := snapshot(t, s.root)
	dest := t.TempDir()
	ctx := context.Background()

	res, err := Pull(ctx, s.source(), dest, Options{Delete: true})
	if err != nil || res.Got != 4 {
		t.Fatalf("first pull: %+v %v", res, err)
	}
	if got := files(t, dest); len(got) != 4 {
		t.Fatalf("files %v", got)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "Artist/Album/01 One.flac"))
	if string(b) != lib["Artist/Album/01 One.flac"] {
		t.Fatal("content differs")
	}

	// Nothing changed: nothing is fetched.
	res, err = Pull(ctx, s.source(), dest, Options{Delete: true})
	if err != nil || res.Got != 0 || res.Kept != 4 {
		t.Fatalf("second pull: %+v %v", res, err)
	}

	// The user's own file in the folder is never touched.
	os.WriteFile(filepath.Join(dest, "mine.mp3"), []byte("mine"), 0o644)

	// On the server: one changed, one added, one removed.
	os.WriteFile(filepath.Join(s.root, "Artist/Album/02 Two.flac"), []byte("two, remastered"), 0o644)
	library(t, s.root, map[string]string{"New/Album/01.flac": "new"})
	os.Remove(filepath.Join(s.root, "Other/Single/a.opus"))
	s.rebuild(t)
	before = snapshot(t, s.root)

	res, err = Pull(ctx, s.source(), dest, Options{Delete: true})
	if err != nil || res.Got != 2 || res.Deleted != 1 {
		t.Fatalf("third pull: %+v %v", res, err)
	}
	want := []string{"Artist/Album/01 One.flac", "Artist/Album/02 Two.flac", "Artist/Album/cover.jpg", "New/Album/01.flac", "mine.mp3"}
	if got := files(t, dest); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("files %v", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "Other")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("emptied folder left behind")
	}
	if !equalMaps(before, snapshot(t, s.root)) {
		t.Fatal("the server's library changed")
	}
}

// failing reads n bytes of the file, then fails, as a dropped connection.
type failing struct {
	DirSource
	after int64
	opens []int64
}

func (f *failing) Open(ctx context.Context, p string, offset int64) (io.ReadCloser, error) {
	f.opens = append(f.opens, offset)
	r, err := f.DirSource.Open(ctx, p, offset)
	if err != nil || f.after < 0 {
		return r, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(io.LimitReader(r, f.after), errReader{}), r}, nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection dropped") }

func TestInterruptedTransferResumes(t *testing.T) {
	s := newServer(t, map[string]string{"A/big.flac": strings.Repeat("0123456789", 10000)})
	dest := t.TempDir()
	src := &failing{DirSource: s.source(), after: 30000}
	res, err := Pull(context.Background(), src, dest, Options{})
	if err == nil || res.Got != 0 {
		t.Fatalf("pull through a dropped connection: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "A/big.flac")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a partial file was put in place")
	}
	// The plan counts only what is left.
	if p, _ := Pull(context.Background(), src, dest, Options{DryRun: true}); p.Bytes != 100000-30000 {
		t.Fatalf("left to fetch %d", p.Bytes)
	}
	src.after = -1
	res, err = Pull(context.Background(), src, dest, Options{})
	if err != nil || res.Got != 1 {
		t.Fatalf("resumed pull: %+v %v", res, err)
	}
	if last := src.opens[len(src.opens)-1]; last != 30000 {
		t.Fatalf("resumed from %d, want 30000", last)
	}
	b, _ := os.ReadFile(filepath.Join(dest, "A/big.flac"))
	if string(b) != strings.Repeat("0123456789", 10000) {
		t.Fatal("resumed file differs")
	}
}

func TestCorruptDownloadIsDiscarded(t *testing.T) {
	s := newServer(t, map[string]string{"A/x.flac": "right contents"})
	// The file changes on the server after the manifest was made.
	os.WriteFile(filepath.Join(s.root, "A/x.flac"), []byte("wrong contents"), 0o644)
	dest := t.TempDir()
	res, err := Pull(context.Background(), s.source(), dest, Options{})
	if err == nil || !strings.Contains(fmt.Sprint(res.Failed["A/x.flac"]), "hash mismatch") {
		t.Fatalf("%+v %v", res, err)
	}
	if got := files(t, dest); len(got) != 0 {
		t.Fatalf("corrupt file kept: %v", got)
	}
}

func TestUnsafeManifestRefused(t *testing.T) {
	for _, p := range []string{"../escape.flac", "/abs.flac", "a/../../b.flac", "a\\b.flac", "a:b.flac", "trailing./x.flac",
		".rgsp-sync/state.json", "", "a//b.flac"} {
		m := Manifest{Version: 1, Files: []Entry{{Path: p, Size: 1, SHA256: strings.Repeat("0", 64)}}}
		if m.Validate() == nil {
			t.Errorf("path %q accepted", p)
		}
	}
	dup := Manifest{Version: 1, Files: []Entry{{Path: "A.flac", SHA256: strings.Repeat("0", 64)}, {Path: "a.flac", SHA256: strings.Repeat("0", 64)}}}
	if dup.Validate() == nil {
		t.Error("names differing only in case accepted")
	}
	ok := Manifest{Version: 1, Files: []Entry{{Path: "Björk/Homogenic/01 Hunter.flac", SHA256: strings.Repeat("a", 64)}}}
	if err := ok.Validate(); err != nil {
		t.Error(err)
	}
}

func TestNoSpaceStopsBeforeDownloading(t *testing.T) {
	s := newServer(t, map[string]string{"A/x.flac": "x"})
	dest := t.TempDir()
	_, err := Pull(context.Background(), s.source(), dest, Options{Reserve: 1 << 62})
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err %v", err)
	}
	if got := files(t, dest); len(got) != 0 {
		t.Fatalf("files %v", got)
	}
}

// Over HTTP: only GETs, ranges honoured, a server ignoring ranges refused.
func TestHTTPSource(t *testing.T) {
	s := newServer(t, map[string]string{"Sigur Rós/( )/01 Untitled #1.flac": strings.Repeat("ab", 40000)})
	var methods []string
	ignoreRange := false
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, s.manifest) })
	files := http.FileServer(http.Dir(s.root))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if ignoreRange {
			r.Header.Del("Range")
		}
		files.ServeHTTP(w, r)
	}))
	ts := httptest.NewServer(mux)
	defer ts.Close()
	src := HTTPSource{Base: ts.URL + "/"}
	dest := t.TempDir()

	// Leave a partial file as an interrupted run would.
	m, _ := src.Manifest(context.Background())
	part := partialPath(dest, m.Files[0])
	os.MkdirAll(filepath.Dir(part), 0o755)
	os.WriteFile(part, []byte(strings.Repeat("ab", 10000)), 0o644)

	ignoreRange = true
	if _, err := Pull(context.Background(), src, dest, Options{}); err == nil {
		t.Fatal("a server ignoring Range was trusted")
	}
	ignoreRange = false
	res, err := Pull(context.Background(), src, dest, Options{})
	if err != nil || res.Got != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Fatalf("method %s used", m)
		}
	}
}

func TestGate(t *testing.T) {
	g := Gate{HomeSSIDs: []string{"home"}, MinBattery: 40,
		SSID:    func() (string, bool) { return "home", true },
		Battery: func() (int, bool, bool) { return 20, true, true }}
	if ok, why := g.Allow(); !ok {
		t.Fatalf("charging at home refused: %s", why)
	}
	g.Battery = func() (int, bool, bool) { return 20, false, true }
	if ok, _ := g.Allow(); ok {
		t.Fatal("low battery allowed")
	}
	g.Battery = func() (int, bool, bool) { return 80, false, true }
	g.SSID = func() (string, bool) { return "cafe", true }
	if ok, _ := g.Allow(); ok {
		t.Fatal("away from home allowed")
	}
	g.SSID = func() (string, bool) { return "", false }
	if ok, _ := g.Allow(); ok {
		t.Fatal("offline allowed")
	}
}

func TestBatteryFromSysfs(t *testing.T) {
	sys := t.TempDir()
	d := filepath.Join(sys, "class", "power_supply", "axp2202-battery")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "type"), []byte("Battery\n"), 0o644)
	os.WriteFile(filepath.Join(d, "capacity"), []byte("57\n"), 0o644)
	os.WriteFile(filepath.Join(d, "status"), []byte("Charging\n"), 0o644)
	if pct, ch, ok := BatteryFrom(sys)(); !ok || pct != 57 || !ch {
		t.Fatalf("%d %v %v", pct, ch, ok)
	}
}

func TestOnCardReplacesWhatExFATRefuses(t *testing.T) {
	if got := onCard("/c", "JPEGMAFIA/LP!/11 - 💯.opus"); got != "/c/JPEGMAFIA/LP!/11 - _.opus" {
		t.Fatalf("emoji: %q", got)
	}
	if got := onCard("/c", "Björk/Jóga – live.opus"); got != "/c/Björk/Jóga – live.opus" {
		t.Fatalf("kept: %q", got)
	}
}

func TestThrottled(t *testing.T) {
	src := bytes.NewReader(make([]byte, 40<<10))
	r := &throttled{ctx: context.Background(), r: src, limit: func() int64 { return 200 << 10 }}
	start := time.Now()
	n, err := io.Copy(io.Discard, r)
	if err != nil || n != 40<<10 {
		t.Fatalf("read %d, %v", n, err)
	}
	if took := time.Since(start); took < 150*time.Millisecond {
		t.Fatalf("40 KB at 200 KB/s took %v", took)
	}
}

func TestWhilePlaying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playing")
	if WhilePlaying(path, 100)() != 0 {
		t.Fatal("limited with no file")
	}
	os.WriteFile(path, []byte("123 playing\n"), 0o644)
	if WhilePlaying(path, 100)() != 100 {
		t.Fatal("not limited while playing")
	}
	os.WriteFile(path, []byte("123 paused\n"), 0o644)
	if WhilePlaying(path, 100)() != 0 {
		t.Fatal("limited while paused")
	}
}

// A server suddenly listing far fewer files deletes nothing, as when its
// disk isn't mounted; the files it still lists sync as usual.
func TestMassDeleteRefused(t *testing.T) {
	files := map[string]string{"Keep/a.opus": "keep"}
	for i := range 80 {
		files[fmt.Sprintf("Gone/%02d.opus", i)] = fmt.Sprint("song ", i)
	}
	s := newServer(t, files)
	dest := t.TempDir()
	ctx := context.Background()
	if _, err := Pull(ctx, s.source(), dest, Options{Delete: true}); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(s.root, "Gone"))
	s.rebuild(t)
	res, err := Pull(ctx, s.source(), dest, Options{Delete: true})
	if err != nil || res.Deleted != 0 {
		t.Fatalf("pull: deleted %d, %v", res.Deleted, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "Gone/00.opus")); err != nil {
		t.Fatal("a file was deleted")
	}
	res, err = Pull(ctx, s.source(), dest, Options{Delete: true, MaxDelete: 1})
	if err != nil || res.Deleted != 80 {
		t.Fatalf("allowed: deleted %d, %v", res.Deleted, err)
	}
}
