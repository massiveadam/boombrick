package musicsync

import (
	"context"
	"crypto/sha256"
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
	"strings"
	"syscall"
	"time"
)

// Source is where the library is pulled from. It is only ever read.
type Source interface {
	// Manifest reads the library's manifest.
	Manifest(ctx context.Context) (Manifest, error)
	// Open reads the file at p, a manifest path, from offset on.
	Open(ctx context.Context, p string, offset int64) (io.ReadCloser, error)
}

// DirSource reads a library from a folder, such as a mounted share, and its
// manifest from a file.
type DirSource struct {
	Root, ManifestPath string
}

// Manifest reads the manifest file.
func (d DirSource) Manifest(context.Context) (Manifest, error) {
	b, err := os.ReadFile(d.ManifestPath)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	return m, json.Unmarshal(b, &m)
}

// Open opens the file read-only, at offset.
func (d DirSource) Open(_ context.Context, p string, offset int64) (io.ReadCloser, error) {
	if err := CheckPath(p); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(d.Root, filepath.FromSlash(p)))
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// HTTPSource reads a library from a read-only HTTP server: the manifest at
// Base + "manifest.json" (or ManifestURL), and each file at Base + its path.
// Only GET requests are made.
type HTTPSource struct {
	Base        string // ends in "/"
	ManifestURL string // "" for Base + "manifest.json"
	Client      *http.Client
}

func (h HTTPSource) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 0} // a whole album can take a while; ctx cancels
}

// Manifest fetches the manifest.
func (h HTTPSource) Manifest(ctx context.Context) (Manifest, error) {
	u := h.ManifestURL
	if u == "" {
		u = h.Base + "manifest.json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Manifest{}, err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("musicsync: manifest: %s", resp.Status)
	}
	var m Manifest
	err = json.NewDecoder(io.LimitReader(resp.Body, 256<<20)).Decode(&m)
	return m, err
}

// Open fetches the file from offset, with a Range request when offset > 0. A
// server that ignores the range is refused, so a resumed file never gets the
// wrong bytes.
func (h HTTPSource) Open(ctx context.Context, p string, offset int64) (io.ReadCloser, error) {
	if err := CheckPath(p); err != nil {
		return nil, err
	}
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Base+strings.Join(parts, "/"), nil)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, err
	}
	want := http.StatusOK
	if offset > 0 {
		want = http.StatusPartialContent
	}
	if resp.StatusCode != want {
		resp.Body.Close()
		return nil, fmt.Errorf("musicsync: %s: %s", p, resp.Status)
	}
	return resp.Body, nil
}

// stateDir is the folder inside the destination where Pull keeps what it
// synced and its partial files. CheckPath keeps the manifest out of it.
const stateDir = ".rgsp-sync"

// State is what Pull has synced into a folder: each file's entry as it was
// when it landed.
type State struct {
	Files map[string]Entry `json:"files"`
}

// Options configure Pull.
type Options struct {
	DryRun bool // work out and report the plan, change nothing
	// Delete removes files Pull synced earlier that the server no longer
	// lists. Files it didn't sync are never removed.
	Delete bool
	// Reserve is space to leave free on the card, in bytes.
	Reserve int64
	Log     *slog.Logger
}

// Plan is what a pull does.
type Plan struct {
	Get    []Entry  // new or changed files
	Delete []string // files synced before that the server dropped
	Bytes  int64    // to download, less what partial files already hold
	Kept   int      // files already up to date
}

// Result is what a pull did.
type Result struct {
	Plan
	Got, Deleted int
	Failed       map[string]error
}

// ErrNoSpace is returned when the card hasn't room for what is to come.
var ErrNoSpace = errors.New("musicsync: not enough free space")

// Pull brings dest up to date with src. It is safe to interrupt at any
// point: a later Pull resumes partial files and redoes nothing finished.
func Pull(ctx context.Context, src Source, dest string, opts Options) (Result, error) {
	log := opts.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	m, err := src.Manifest(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := m.Validate(); err != nil {
		return Result{}, err
	}
	st, err := loadState(dest)
	if err != nil {
		return Result{}, err
	}
	plan := makePlan(m, st, dest, opts.Delete)
	res := Result{Plan: plan, Failed: map[string]error{}}
	log.Info("sync plan", "get", len(plan.Get), "delete", len(plan.Delete), "bytes", plan.Bytes, "kept", plan.Kept)
	if opts.DryRun {
		return res, nil
	}
	if free, ok := freeSpace(dest); ok && free-opts.Reserve < plan.Bytes {
		return res, fmt.Errorf("%w: %d bytes to download, %d free beyond the reserve", ErrNoSpace, plan.Bytes, free-opts.Reserve)
	}
	// The state is saved every stateEvery and at the end, not after every
	// file: rewritten and synced each time, on a FAT card it cost more than
	// the music (a 14,000-file library filled at a third of the card's speed).
	// A file in place but not yet in the saved state is only fetched again.
	saved := time.Now()
	stopped := func(err error) (Result, error) {
		if serr := saveState(dest, st); serr != nil {
			err = errors.Join(err, serr)
		}
		return res, err
	}
	for _, e := range plan.Get {
		if ctx.Err() != nil {
			return stopped(ctx.Err())
		}
		if err := fetch(ctx, src, dest, e); err != nil {
			log.Warn("sync file", "path", e.Path, "err", err)
			res.Failed[e.Path] = err
			if ctx.Err() != nil {
				return stopped(ctx.Err())
			}
			continue
		}
		st.Files[e.Path] = e
		res.Got++
		if time.Since(saved) >= stateEvery {
			if err := saveState(dest, st); err != nil {
				return res, err
			}
			saved = time.Now()
		}
	}
	for _, p := range plan.Delete {
		full := filepath.Join(dest, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			res.Failed[p] = err
			continue
		}
		removeEmptyParents(dest, filepath.Dir(full))
		delete(st.Files, p)
		res.Deleted++
	}
	if err := saveState(dest, st); err != nil {
		return res, err
	}
	if len(res.Failed) > 0 {
		return res, fmt.Errorf("musicsync: %d file(s) failed", len(res.Failed))
	}
	return res, nil
}

// makePlan compares the manifest with what was synced and what is on disk.
// A file on disk counts as up to date only if Pull synced it with the same
// hash and its size still matches.
// stateEvery is how often, at most, a pull saves what it has synced so far.
const stateEvery = 5 * time.Second

func makePlan(m Manifest, st State, dest string, del bool) Plan {
	var p Plan
	listed := map[string]bool{}
	for _, e := range m.Files {
		listed[e.Path] = true
		full := filepath.Join(dest, filepath.FromSlash(e.Path))
		if old, ok := st.Files[e.Path]; ok && old.SHA256 == e.SHA256 {
			if info, err := os.Stat(full); err == nil && info.Size() == e.Size {
				p.Kept++
				continue
			}
		}
		p.Get = append(p.Get, e)
		have := int64(0)
		if info, err := os.Stat(partialPath(dest, e)); err == nil && info.Size() <= e.Size {
			have = info.Size()
		}
		p.Bytes += e.Size - have
	}
	if del {
		for path := range st.Files {
			if !listed[path] {
				p.Delete = append(p.Delete, path)
			}
		}
	}
	return p
}

func partialPath(dest string, e Entry) string {
	return filepath.Join(dest, stateDir, "partial", e.SHA256+".part")
}

// fetch downloads e, resuming its partial file, checks its hash and renames
// it into place.
func fetch(ctx context.Context, src Source, dest string, e Entry) error {
	part := partialPath(dest, e)
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	have, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if have > e.Size {
		if err := f.Truncate(0); err != nil {
			return err
		}
		have, _ = f.Seek(0, io.SeekStart)
	}
	if have < e.Size {
		r, err := src.Open(ctx, e.Path, have)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(r, e.Size-have))
		r.Close()
		if err != nil {
			f.Sync() // keep what came, for the next run
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		f.Close()
		os.Remove(part) // corrupt or changed under us: start again next time
		return fmt.Errorf("musicsync: %s: hash mismatch, discarded", e.Path)
	}
	f.Close()
	full := filepath.Join(dest, filepath.FromSlash(e.Path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.Rename(part, full); err != nil {
		return err
	}
	mt := time.Unix(e.MTime, 0)
	_ = os.Chtimes(full, mt, mt)
	syncDir(filepath.Dir(full))
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

// removeEmptyParents removes dir and its parents up to dest while empty.
func removeEmptyParents(dest, dir string) {
	root := filepath.Clean(dest)
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

func loadState(dest string) (State, error) {
	st := State{Files: map[string]Entry{}}
	b, err := os.ReadFile(filepath.Join(dest, stateDir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return State{Files: map[string]Entry{}}, fmt.Errorf("musicsync: state damaged, resync needed: %w", err)
	}
	if st.Files == nil {
		st.Files = map[string]Entry{}
	}
	for p := range st.Files {
		if CheckPath(p) != nil {
			delete(st.Files, p) // never act on a path that could leave dest
		}
	}
	return st, nil
}

func saveState(dest string, st State) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dest, stateDir, "state.json"), b)
}

// freeSpace is the space free on dest's file system.
func freeSpace(dest string) (int64, bool) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, false
	}
	var s syscall.Statfs_t
	if err := syscall.Statfs(dest, &s); err != nil {
		return 0, false
	}
	return int64(s.Bavail) * int64(s.Bsize), true
}
