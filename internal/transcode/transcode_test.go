package transcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake encodes by writing "opus:" and the source's bytes, and reports the
// codec named in a file's first line ("codec=alac").
type fake struct {
	mu      sync.Mutex
	encoded []string
	fail    string // a source base name to fail on
	set     string
}

func (f *fake) Codec(_ context.Context, src string) (string, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if c, ok := strings.CutPrefix(strings.SplitN(string(b), "\n", 2)[0], "codec="); ok {
		return c, nil
	}
	return "", errors.New("no audio stream")
}

func (f *fake) Encode(_ context.Context, src, dst string) error {
	if filepath.Base(src) == f.fail {
		return errors.New("bad file")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.encoded = append(f.encoded, filepath.Base(src))
	f.mu.Unlock()
	return os.WriteFile(dst, append([]byte("opus:"), b...), 0o644)
}

func (f *fake) Picture(_ context.Context, src, dst string) (string, error) {
	b, _ := os.ReadFile(src)
	if !strings.Contains(string(b), "picture") {
		return "", nil
	}
	return ".jpg", os.WriteFile(dst+".jpg", []byte("jpeg"), 0o644)
}

func (f *fake) Settings() string {
	if f.set == "" {
		return "fake 256"
	}
	return f.set
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			r, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(r))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// snapshot is every file under root with its bytes and time.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, f := range files(t, root) {
		p := filepath.Join(root, f)
		b, _ := os.ReadFile(p)
		info, _ := os.Stat(p)
		m[f] = string(b) + "|" + info.ModTime().String()
	}
	return m
}

func library(t *testing.T) string {
	src := t.TempDir()
	write(t, src, "A/One/01 Song.flac", "flac one")
	write(t, src, "A/One/02 Song.flac", "flac two")
	write(t, src, "A/One/cover.jpg", "cover")
	write(t, src, "A/One/booklet-3.jpg", "scan") // not a cover: left out
	write(t, src, "A/One/01 Song.lrc", "lyrics")
	write(t, src, "B/Two/01 Lossy.m4a", "codec=aac\npicture") // the album's first track gives its cover
	write(t, src, "B/Two/02 Apple.m4a", "codec=alac\n")
	write(t, src, "B/Two/03 Old.mp3", "mp3")
	write(t, src, "C/Three/01 Hi.dsf", "dsd")
	write(t, src, ".hidden/x.flac", "hidden")
	return src
}

func TestRunMakesTheCopy(t *testing.T) {
	src := library(t)
	before := snapshot(t, src)
	dst := filepath.Join(t.TempDir(), "copy")
	enc := &fake{}
	res, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: enc, Jobs: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		".transcode-state.json",
		"A/One/01 Song.lrc", "A/One/01 Song.opus", "A/One/02 Song.opus", "A/One/cover.jpg",
		"B/Two/01 Lossy.m4a", "B/Two/02 Apple.opus", "B/Two/03 Old.mp3", "B/Two/cover.jpg",
		"C/Three/01 Hi.opus",
	}
	if got := files(t, dst); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("copy holds\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if res.Made != 9 || len(res.Failed) != 0 {
		t.Fatalf("made %d, failed %v", res.Made, res.Failed)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "B/Two/01 Lossy.m4a")); string(b) != "codec=aac\npicture" {
		t.Fatalf("AAC was not copied as it is: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "A/One/01 Song.opus")); string(b) != "opus:flac one" {
		t.Fatalf("FLAC was not encoded: %q", b)
	}
	si, _ := os.Stat(filepath.Join(src, "A/One/01 Song.flac"))
	di, _ := os.Stat(filepath.Join(dst, "A/One/01 Song.opus"))
	if !si.ModTime().Equal(di.ModTime()) {
		t.Fatalf("copy's time %v, source's %v", di.ModTime(), si.ModTime())
	}
	if after := snapshot(t, src); len(after) != len(before) {
		t.Fatal("the library changed")
	} else {
		for k, v := range before {
			if after[k] != v {
				t.Fatalf("the library's %s changed", k)
			}
		}
	}

	// A second run does nothing.
	enc.encoded = nil
	res, err = Run(context.Background(), Options{Src: src, Dst: dst, Encoder: enc})
	if err != nil {
		t.Fatal(err)
	}
	// C/Three's cover was looked for and isn't there: not looked for again.
	if len(res.Make) != 0 || len(res.Remove) != 0 || res.Kept != 10 || len(enc.encoded) != 0 {
		t.Fatalf("second run: make %v remove %v kept %d encoded %v", res.Make, res.Remove, res.Kept, enc.encoded)
	}
}

func TestRunFollowsChanges(t *testing.T) {
	src := library(t)
	dst := t.TempDir()
	enc := &fake{}
	if _, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: enc}); err != nil {
		t.Fatal(err)
	}
	write(t, dst, "A/One/mine.txt", "not made by the tool") // never touched

	// A retag (new bytes and time), a removal, a new album, a lost file.
	write(t, src, "A/One/02 Song.flac", "flac two retagged")
	later := time.Now().Add(time.Hour)
	os.Chtimes(filepath.Join(src, "A/One/02 Song.flac"), later, later)
	os.RemoveAll(filepath.Join(src, "C"))
	write(t, src, "D/Four/01 New.flac", "new")
	os.Remove(filepath.Join(dst, "B/Two/03 Old.mp3"))

	enc.encoded = nil
	res, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: enc})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(enc.encoded)
	if strings.Join(enc.encoded, ",") != "01 New.flac,02 Song.flac" {
		t.Fatalf("encoded %v", enc.encoded)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "A/One/02 Song.opus")); string(b) != "opus:flac two retagged" {
		t.Fatalf("retagged track not re-encoded: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "C")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the removed album's folder is still in the copy")
	}
	if _, err := os.Stat(filepath.Join(dst, "B/Two/03 Old.mp3")); err != nil {
		t.Fatal("a lost file was not made again")
	}
	if _, err := os.Stat(filepath.Join(dst, "A/One/mine.txt")); err != nil {
		t.Fatal("a file the tool didn't make was removed")
	}
	if res.Kept != 6 {
		t.Fatalf("kept %d, want 6", res.Kept)
	}
}

func TestSettingsChangeReencodes(t *testing.T) {
	src := library(t)
	dst := t.TempDir()
	if _, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: &fake{}}); err != nil {
		t.Fatal(err)
	}
	enc := &fake{set: "fake 160"}
	if _, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: enc}); err != nil {
		t.Fatal(err)
	}
	if len(enc.encoded) != 4 {
		t.Fatalf("encoded %v, want the 4 lossless tracks", enc.encoded)
	}
}

func TestFailureIsRetriedAndOthersKept(t *testing.T) {
	src := library(t)
	dst := t.TempDir()
	res, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: &fake{fail: "01 Hi.dsf"}})
	if err == nil || len(res.Failed) != 1 || res.Made != 8 {
		t.Fatalf("err %v failed %v made %d", err, res.Failed, res.Made)
	}
	for _, f := range files(t, dst) {
		if strings.Contains(f, ".part") {
			t.Fatalf("partial file left: %s", f)
		}
	}
	enc := &fake{}
	if _, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: enc}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(enc.encoded, ",") != "01 Hi.dsf" {
		t.Fatalf("retry encoded %v", enc.encoded)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	src := library(t)
	dst := filepath.Join(t.TempDir(), "copy")
	res, err := Run(context.Background(), Options{Src: src, Dst: dst, Encoder: &fake{}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Make) != 10 { // 9 files, and a look for C/Three's cover
		t.Fatalf("plan makes %d, want 10", len(res.Make))
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry run made the copy's folder")
	}
}

func TestRefusesOverlap(t *testing.T) {
	src := library(t)
	for _, o := range []Options{
		{Src: src, Dst: filepath.Join(src, "copy")},
		{Src: filepath.Join(src, "A"), Dst: src},
		{Src: src, Dst: src},
		{Src: src, Dst: t.TempDir(), State: filepath.Join(src, "state.json")},
	} {
		o.Encoder = &fake{}
		if _, err := Run(context.Background(), o); !errors.Is(err, ErrOverlap) {
			t.Errorf("%+v: err %v, want ErrOverlap", o, err)
		}
	}
}

func TestCaseCollisionSkipped(t *testing.T) {
	src := t.TempDir()
	write(t, src, "A/01 Song.flac", "flac")
	write(t, src, "A/01 song.opus", "opus") // same place on FAT once the flac is .opus
	res, err := Run(context.Background(), Options{Src: src, Dst: t.TempDir(), Encoder: &fake{}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skip) != 1 || len(res.Make) != 2 || res.Make[0].Out != "A/01 Song.opus" {
		t.Fatalf("make %v skip %v", res.Make, res.Skip)
	}
}
