// Package musicsync copies a music library from a server that stays
// authoritative onto the player's card: one-way, resumable, verified.
//
// The server side only reads. BuildManifest walks the library and lists every
// file with its size, modification time and SHA-256; the manifest is written
// somewhere outside the library, and served with the files by any read-only
// HTTP server (or read from a mounted share).
//
// The player side, Pull, fetches the manifest, works out what is new or
// changed, downloads each file to a partial file that a later run resumes,
// checks its SHA-256, and only then renames it into place. It deletes only
// files that it synced itself and that the server no longer lists; anything
// else in the folder is never touched.
package musicsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestVersion is the manifest format this package writes and reads.
const ManifestVersion = 1

// Entry is one file of the library.
type Entry struct {
	Path   string `json:"path"` // relative, slash-separated
	Size   int64  `json:"size"`
	MTime  int64  `json:"mtime"` // Unix seconds
	SHA256 string `json:"sha256"`
}

// Manifest lists a library.
type Manifest struct {
	Version   int       `json:"version"`
	Generated time.Time `json:"generated"`
	Files     []Entry   `json:"files"`
}

// DefaultExtensions are the files a manifest lists: music, covers, lyrics
// and cue sheets.
var DefaultExtensions = []string{".flac", ".mp3", ".m4a", ".aac", ".opus", ".ogg",
	".jpg", ".jpeg", ".png", ".webp", ".lrc", ".cue"}

// hashCache remembers files' hashes by path, size and time, so building a
// manifest again reads only what changed.
type hashCache map[string]Entry

// BuildOptions configure BuildManifest.
type BuildOptions struct {
	Extensions []string // nil: DefaultExtensions
	// Cache is a file that keeps hashes between runs; it must be outside the
	// library. "" for none.
	Cache string
}

// BuildManifest lists the files under root. It only reads root. Hidden files
// and folders (starting with ".") are left out.
func BuildManifest(root string, opts BuildOptions) (Manifest, error) {
	exts := opts.Extensions
	if exts == nil {
		exts = DefaultExtensions
	}
	if opts.Cache != "" && inside(root, opts.Cache) {
		return Manifest{}, fmt.Errorf("musicsync: the hash cache %s is inside the library %s, which is never written", opts.Cache, root)
	}
	cache := hashCache{}
	if opts.Cache != "" {
		if b, err := os.ReadFile(opts.Cache); err == nil {
			_ = json.Unmarshal(b, &cache)
		}
	}
	m := Manifest{Version: ManifestVersion, Generated: time.Now().UTC()}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() || !hasExt(d.Name(), exts) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		e := Entry{Path: filepath.ToSlash(rel), Size: info.Size(), MTime: info.ModTime().Unix()}
		if c, ok := cache[e.Path]; ok && c.Size == e.Size && c.MTime == e.MTime && c.SHA256 != "" {
			e.SHA256 = c.SHA256
		} else {
			sum, err := hashFile(p)
			if err != nil {
				return err
			}
			e.SHA256 = sum
		}
		cache[e.Path] = e
		m.Files = append(m.Files, e)
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if opts.Cache != "" {
		keep := hashCache{}
		for _, e := range m.Files {
			keep[e.Path] = e
		}
		b, _ := json.Marshal(keep)
		if err := writeAtomic(opts.Cache, b); err != nil {
			return Manifest{}, err
		}
	}
	return m, nil
}

func hasExt(name string, exts []string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// inside reports whether p is root or under it.
func inside(root, p string) bool {
	r, err1 := filepath.Abs(root)
	q, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return true // can't tell: assume the worst
	}
	rel, err := filepath.Rel(r, q)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ErrUnsafePath is returned for a manifest path that could leave the folder
// it is synced into, or that a FAT or exFAT card can't hold.
var ErrUnsafePath = errors.New("musicsync: unsafe path")

// CheckPath reports whether p is a path the player may write: relative,
// slash-separated, clean, inside the folder, and with no characters FAT and
// exFAT refuse.
func CheckPath(p string) error {
	switch {
	case p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\"):
	case path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../"):
	case strings.ContainsAny(p, "\x00:*?\"<>|"):
	case strings.HasPrefix(p, ".rgsp-sync"):
	default:
		for _, part := range strings.Split(p, "/") {
			if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return fmt.Errorf("%w: %q", ErrUnsafePath, p) // FAT drops trailing dots and spaces
			}
			for _, r := range part {
				if r < 0x20 {
					return fmt.Errorf("%w: %q", ErrUnsafePath, p)
				}
			}
		}
		return nil
	}
	return fmt.Errorf("%w: %q", ErrUnsafePath, p)
}

// Validate checks a manifest read from the network before anything acts on it.
func (m Manifest) Validate() error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("musicsync: manifest version %d, want %d", m.Version, ManifestVersion)
	}
	seen := map[string]bool{}
	for _, e := range m.Files {
		if err := CheckPath(e.Path); err != nil {
			return err
		}
		key := strings.ToLower(e.Path) // FAT and exFAT ignore case
		if seen[key] {
			return fmt.Errorf("musicsync: %q is listed twice (case ignored)", e.Path)
		}
		seen[key] = true
		if e.Size < 0 || len(e.SHA256) != 64 {
			return fmt.Errorf("musicsync: bad entry for %q", e.Path)
		}
		if _, err := hex.DecodeString(e.SHA256); err != nil {
			return fmt.Errorf("musicsync: bad hash for %q", e.Path)
		}
	}
	return nil
}

// writeAtomic writes b to p through a synced temporary file and a rename.
func writeAtomic(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(b)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, p)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
