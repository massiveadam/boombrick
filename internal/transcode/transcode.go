// Package transcode keeps a smaller copy of a music library for the player:
// lossless tracks encoded as Opus, the lossy tracks the player can already
// play copied as they are, and the albums' cover images and lyrics alongside.
//
// The library is only ever read. Everything is written under the copy's
// folder, which must not overlap the library, and a run touches only files
// that an earlier run made there, as recorded in its state file. A run can
// be stopped at any point: each file is written beside its place and renamed
// in, and the next run redoes only what is missing or changed.
package transcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Action is what is done with a file of the library.
type Action string

const (
	Encode Action = "encode" // to Opus
	Copy   Action = "copy"   // as it is
	Cover  Action = "cover"  // the embedded picture, for an album with no cover file
)

// Encoder does the work that needs a codec. FFmpeg is the real one.
type Encoder interface {
	// Codec is the codec of src's first audio stream ("flac", "alac",
	// "aac", ...).
	Codec(ctx context.Context, src string) (string, error)
	// Encode writes src as Ogg Opus to dst, its tags kept.
	Encode(ctx context.Context, src, dst string) error
	// Picture writes src's embedded picture to dst, a file name without an
	// extension, and returns the extension it chose ("" for no picture).
	Picture(ctx context.Context, src, dst string) (string, error)
	// Settings names the encoding: a change re-encodes everything.
	Settings() string
}

// Options configure Run.
type Options struct {
	Src, Dst string // the library (read only), and the copy
	// State is the file that records what was made; "" for
	// <Dst>/.transcode-state.json. It must be outside Src.
	State   string
	Encoder Encoder
	Jobs    int  // encodes at once; 0 for 1
	DryRun  bool // plan and report only
	Log     *slog.Logger
}

// Record is a file the copy holds, by its path in the copy.
type Record struct {
	Src    string `json:"src"` // path in the library
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"` // the source's, Unix nanoseconds
	Action Action `json:"action"`
	Codec  string `json:"codec,omitempty"`
	Set    string `json:"settings,omitempty"`
	// OutSize is the made file's size. Files aren't synced one by one (on
	// a USB disk with exFAT that stalls everything else on it), so one cut
	// short by a power loss shows as a size that doesn't match, and is made
	// again.
	OutSize int64 `json:"out_size,omitempty"`
	// None marks a cover looked for and not found, so it isn't looked for
	// again until the track changes.
	None bool `json:"none,omitempty"`
}

type stateFile struct {
	Version int               `json:"version"`
	Files   map[string]Record `json:"files"` // by path in the copy
}

// Item is one file to make.
type Item struct {
	Out string // path in the copy, slash-separated
	Record
}

// Plan is what a run does.
type Plan struct {
	Make   []Item
	Remove []string // files made earlier whose source is gone or changed
	Kept   int
	Skip   []string // library files left out, with why
}

// Result is what a run did.
type Result struct {
	Plan
	Made, Removed int
	Failed        map[string]error
}

// Which files of the library are what.
var (
	audioExts  = []string{".flac", ".wav", ".aif", ".aiff", ".dsf", ".dff", ".wv", ".ape", ".m4a", ".mp4", ".alac", ".mp3", ".aac", ".opus", ".ogg", ".oga"}
	coverNames = []string{"cover", "folder", "front"}
	imageExts  = []string{".jpg", ".jpeg", ".png", ".webp"}
	// playable are the lossy codecs the player decodes, copied as they are:
	// encoding them again would only lose more.
	playable = []string{"mp3", "aac", "opus"}
	// probeExts are containers that may hold a lossless or a lossy codec.
	probeExts = []string{".m4a", ".mp4", ".ogg", ".oga"}
)

// ErrOverlap is returned when the copy and the library overlap.
var ErrOverlap = errors.New("transcode: the copy and the library overlap")

// Run brings the copy up to date with the library.
func Run(ctx context.Context, o Options) (Result, error) {
	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Src == "" || o.Dst == "" || o.Encoder == nil {
		return Result{}, errors.New("transcode: Src, Dst and Encoder are needed")
	}
	if overlap(o.Src, o.Dst) {
		return Result{}, fmt.Errorf("%w: %s, %s", ErrOverlap, o.Src, o.Dst)
	}
	if o.State == "" {
		o.State = filepath.Join(o.Dst, ".transcode-state.json")
	}
	if overlap(o.Src, o.State) {
		return Result{}, fmt.Errorf("%w: the state file %s is in the library", ErrOverlap, o.State)
	}
	st, err := load(o.State)
	if err != nil {
		return Result{}, err
	}
	plan, err := makePlan(ctx, o, st)
	if err != nil {
		return Result{}, err
	}
	res := Result{Plan: plan, Failed: map[string]error{}}
	log.Info("transcode plan", "make", len(plan.Make), "remove", len(plan.Remove), "kept", plan.Kept, "skipped", len(plan.Skip))
	if o.DryRun {
		return res, nil
	}

	for _, p := range plan.Remove {
		full := filepath.Join(o.Dst, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			res.Failed[p] = err
			continue
		}
		removeEmptyParents(o.Dst, filepath.Dir(full))
		delete(st.Files, p)
		res.Removed++
	}

	var mu sync.Mutex
	lastSave := time.Now()
	done := func(it Item, out string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			log.Warn("transcode file", "path", it.Src, "err", err)
			res.Failed[it.Out] = err
			return
		}
		if out == "" {
			it.None = true
			st.Files[it.Out] = it.Record
			return
		}
		if info, err := os.Stat(filepath.Join(o.Dst, filepath.FromSlash(out))); err == nil {
			it.OutSize = info.Size()
		}
		st.Files[out] = it.Record
		res.Made++
		if time.Since(lastSave) > 10*time.Second {
			if err := save(o.State, st); err != nil {
				log.Warn("transcode state", "err", err)
			}
			lastSave = time.Now()
			log.Info("transcode progress", "made", res.Made, "of", len(plan.Make), "failed", len(res.Failed))
		}
	}
	jobs := max(o.Jobs, 1)
	work := make(chan Item)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range work {
				out, err := make1(ctx, o, it)
				done(it, out, err)
			}
		}()
	}
	for _, it := range plan.Make {
		if ctx.Err() != nil {
			break
		}
		work <- it
	}
	close(work)
	wg.Wait()
	if err := save(o.State, st); err != nil {
		return res, err
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if len(res.Failed) > 0 {
		return res, fmt.Errorf("transcode: %d file(s) failed", len(res.Failed))
	}
	return res, nil
}

// makePlan walks the library and works out each file's place in the copy.
func makePlan(ctx context.Context, o Options, st stateFile) (Plan, error) {
	var p Plan
	known := map[string]Record{} // by path in the library
	for _, r := range st.Files {
		known[r.Src] = r
	}
	want := map[string]Record{}  // by path in the copy
	taken := map[string]string{} // lower-cased path in the copy: the source that has it
	claim := func(out string, r Record) bool {
		k := strings.ToLower(out) // FAT and exFAT ignore case
		if other, ok := taken[k]; ok {
			p.Skip = append(p.Skip, fmt.Sprintf("%s: would be %s, which %s already is", r.Src, out, other))
			return false
		}
		taken[k] = r.Src
		want[out] = r
		return true
	}
	err := filepath.WalkDir(o.Src, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if full != o.Src && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		ents, err := os.ReadDir(full)
		if err != nil {
			return err
		}
		var tracks []string
		hasCover := false
		for _, e := range ents {
			name := e.Name()
			if !e.Type().IsRegular() || strings.HasPrefix(name, ".") {
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			rel := relSlash(o.Src, filepath.Join(full, name))
			info, err := e.Info()
			if err != nil {
				return err
			}
			r := Record{Src: rel, Size: info.Size(), MTime: info.ModTime().UnixNano()}
			switch {
			case slices.Contains(audioExts, ext):
				tracks = append(tracks, name)
				codec, err := codecOf(ctx, o, known, filepath.Join(full, name), r, ext)
				if err != nil {
					p.Skip = append(p.Skip, fmt.Sprintf("%s: %v", rel, err))
					continue
				}
				r.Codec = codec
				out := rel
				if slices.Contains(playable, codec) {
					r.Action = Copy
				} else {
					r.Action, r.Set = Encode, o.Encoder.Settings()
					out = strings.TrimSuffix(rel, filepath.Ext(rel)) + ".opus"
				}
				claim(out, r)
			case slices.Contains(imageExts, ext) && slices.Contains(coverNames, strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))):
				r.Action = Copy
				if claim(rel, r) {
					hasCover = true
				}
			case ext == ".lrc":
				r.Action = Copy
				claim(rel, r)
			}
		}
		if len(tracks) > 0 && !hasCover {
			// The player looks for a cover file first; the Opus files are
			// made without their embedded pictures, so give the album one.
			sort.Strings(tracks)
			first := filepath.Join(full, tracks[0])
			info, err := os.Stat(first)
			if err == nil {
				dir := relSlash(o.Src, full)
				out := "cover"
				if dir != "." {
					out = dir + "/cover"
				}
				claim(out, Record{Src: relSlash(o.Src, first), Size: info.Size(), MTime: info.ModTime().UnixNano(), Action: Cover})
			}
		}
		return nil
	})
	if err != nil {
		return Plan{}, err
	}

	// A cover's place has no extension until it is made: the state records
	// it with one.
	for out, r := range st.Files {
		key := out
		if r.Action == Cover {
			key = strings.TrimSuffix(out, path.Ext(out))
		}
		w, ok := want[key]
		if !ok || !same(w, r) {
			p.Remove = append(p.Remove, out)
			continue
		}
		if info, err := os.Stat(filepath.Join(o.Dst, filepath.FromSlash(out))); !r.None && (err != nil || r.OutSize > 0 && info.Size() != r.OutSize) {
			p.Remove = append(p.Remove, out) // made before, since lost or cut short: make it again
			continue
		}
		delete(want, key)
		p.Kept++
	}
	for out, r := range want {
		p.Make = append(p.Make, Item{Out: out, Record: r})
	}
	sort.Slice(p.Make, func(i, j int) bool { return p.Make[i].Out < p.Make[j].Out })
	sort.Strings(p.Remove)
	return p, nil
}

// codecOf names a track's codec: from what the state recorded for the same
// file, from its extension, or by asking the encoder.
func codecOf(ctx context.Context, o Options, known map[string]Record, full string, r Record, ext string) (string, error) {
	if old, ok := known[r.Src]; ok && old.Size == r.Size && old.MTime == r.MTime && old.Codec != "" {
		return old.Codec, nil
	}
	switch ext {
	case ".mp3":
		return "mp3", nil
	case ".aac":
		return "aac", nil
	case ".opus":
		return "opus", nil
	case ".flac":
		return "flac", nil
	}
	if !slices.Contains(probeExts, ext) {
		return strings.TrimPrefix(ext, "."), nil // lossless or not decodable here: encode
	}
	return o.Encoder.Codec(ctx, full)
}

// same reports whether a and b record the same making of the same source.
func same(a, b Record) bool {
	return a.Src == b.Src && a.Size == b.Size && a.MTime == b.MTime && a.Action == b.Action && a.Set == b.Set
}

// make1 makes one file of the copy, written beside its place and renamed in,
// and returns its path in the copy: "" when there was nothing to make.
func make1(ctx context.Context, o Options, it Item) (string, error) {
	src := filepath.Join(o.Src, filepath.FromSlash(it.Src))
	out := filepath.Join(o.Dst, filepath.FromSlash(it.Out))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".part")
	defer os.Remove(tmp)
	switch it.Action {
	case Copy:
		if err := copyFile(src, tmp); err != nil {
			return "", err
		}
	case Encode:
		if err := o.Encoder.Encode(ctx, src, tmp); err != nil {
			return "", err
		}
	case Cover:
		ext, err := o.Encoder.Picture(ctx, src, tmp)
		if err != nil {
			return "", err
		}
		if ext == "" {
			return "", nil // no picture to give: nothing made, nothing recorded
		}
		tmp += ext
		defer os.Remove(tmp)
		out += ext
		it.Out += ext
	}
	mt := time.Unix(0, it.MTime)
	_ = os.Chtimes(tmp, mt, mt)
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return it.Out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // read only
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func relSlash(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// overlap reports whether a and b are the same or one is inside the other.
func overlap(a, b string) bool {
	return within(a, b) || within(b, a)
}

func within(root, p string) bool {
	r, err1 := filepath.Abs(root)
	q, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return true // can't tell: assume the worst
	}
	if rr, err := filepath.EvalSymlinks(r); err == nil {
		r = rr
	}
	if qq, err := filepath.EvalSymlinks(q); err == nil {
		q = qq
	}
	rel, err := filepath.Rel(r, q)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func removeEmptyParents(root, dir string) {
	root = filepath.Clean(root)
	for dir = filepath.Clean(dir); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			return
		}
	}
}

func load(p string) (stateFile, error) {
	st := stateFile{Version: 1, Files: map[string]Record{}}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("transcode: state %s damaged: %w", p, err)
	}
	if st.Files == nil {
		st.Files = map[string]Record{}
	}
	for out := range st.Files {
		if out == "" || strings.HasPrefix(out, "/") || strings.Contains(out, "..") {
			delete(st.Files, out) // never act on a path that could leave the copy
		}
	}
	return st, nil
}

func save(p string, st stateFile) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}
