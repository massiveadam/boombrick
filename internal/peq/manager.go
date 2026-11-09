package peq

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Files the Manager keeps in its folder.
const (
	currentFile = "current.txt" // the profile playing, as Equalizer APO text
	stateFile   = "state.json"  // bypass and the preset it came from
	presetDir   = "presets"     // saved presets, one APO file each
	outputDir   = "outputs"     // each output's own profile: <key>.txt, and <key>.json its state
)

// saveAfter is how long after the last change the current profile is written:
// holding a button down changes it many times a second.
const saveAfter = time.Second

// Default is the profile a new install starts with: ten flat bands at octave
// centres, shelves at the ends, ready to be shaped.
func Default() Profile {
	p := Profile{Name: "Flat"}
	p.Filters = append(p.Filters, Filter{On: true, Kind: LowShelf, Freq: 32, Q: DefaultQ})
	for _, f := range []float64{64, 125, 250, 500, 1000, 2000, 4000, 8000} {
		p.Filters = append(p.Filters, Filter{On: true, Kind: Peak, Freq: f, Q: 1.41})
	}
	p.Filters = append(p.Filters, Filter{On: true, Kind: HighShelf, Freq: 16000, Q: DefaultQ})
	return p
}

type state struct {
	Bypass bool   `json:"bypass"`
	Preset string `json:"preset,omitempty"`
	// Output is the output the profile playing is kept for (SetOutput); ""
	// before any was named.
	Output string `json:"output,omitempty"`
}

// Manager keeps the EQ: the profile playing, whether it is bypassed, and the
// saved presets, in a folder, and hands every change to a Processor. Its
// methods are safe for concurrent use.
type Manager struct {
	dir     string
	imports []string // folders searched for presets to import
	proc    *Processor

	mu      sync.Mutex
	cur     Profile
	st      state
	timer   *time.Timer
	saveErr error
}

// Open reads the EQ kept in dir, creating it with Default the first time, and
// sets proc to it. imports are folders that Imports lists presets from.
func Open(dir string, proc *Processor, imports ...string) (*Manager, error) {
	if err := os.MkdirAll(filepath.Join(dir, presetDir), 0o755); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, imports: imports, proc: proc, cur: Default()}
	if b, err := os.ReadFile(filepath.Join(dir, stateFile)); err == nil {
		_ = json.Unmarshal(b, &m.st) // a damaged state file starts on, from the defaults
	}
	if f, err := os.Open(filepath.Join(dir, currentFile)); err == nil {
		p, _, perr := ParseAPO(f)
		f.Close()
		if perr == nil && p.Validate() == nil {
			p.Name = m.st.Preset
			m.cur = p
		}
	}
	m.apply()
	return m, nil
}

// Processor is the processor the manager drives.
func (m *Manager) Processor() *Processor { return m.proc }

// Profile is the profile playing.
func (m *Manager) Profile() Profile {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cur.Clone()
}

// Bypassed reports whether the EQ is switched off.
func (m *Manager) Bypassed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st.Bypass
}

// Name is the preset the profile playing came from, or "Custom" once changed.
func (m *Manager) Name() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Preset == "" {
		return "Custom"
	}
	return m.st.Preset
}

// SetProfile plays p from now and saves it shortly. A profile that doesn't
// validate is refused. The preset name is cleared: it is a custom profile now.
func (m *Manager) SetProfile(p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.cur, m.st.Preset = p.Clone(), ""
	m.cur.Name = ""
	m.mu.Unlock()
	m.apply()
	m.saveSoon()
	return nil
}

// SetBypass switches the EQ off (true) or on.
func (m *Manager) SetBypass(on bool) {
	m.mu.Lock()
	m.st.Bypass = on
	m.mu.Unlock()
	m.apply()
	m.saveSoon()
}

// Stats are the processor's clipping and peak.
func (m *Manager) Stats() Stats { return m.proc.Stats() }

func (m *Manager) apply() {
	m.mu.Lock()
	p, b := m.cur.Clone(), m.st.Bypass
	m.mu.Unlock()
	if m.proc != nil {
		m.proc.Set(p, b)
	}
}

func (m *Manager) saveSoon() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(saveAfter, func() { _ = m.Flush() })
}

// Flush writes the profile playing and the state now, as before quitting or
// handing the music to the background player, which reads them.
func (m *Manager) Flush() error {
	m.mu.Lock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	cur, st := m.cur.Clone(), m.st
	m.mu.Unlock()
	cur.Name = st.Preset
	err := writeAtomic(filepath.Join(m.dir, currentFile), []byte(FormatAPO(cur)))
	if err == nil {
		b, _ := json.Marshal(st)
		err = writeAtomic(filepath.Join(m.dir, stateFile), b)
	}
	m.mu.Lock()
	m.saveErr = err
	m.mu.Unlock()
	return err
}

// Presets are the saved presets' names, sorted.
func (m *Manager) Presets() []string {
	ents, err := os.ReadDir(filepath.Join(m.dir, presetDir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".txt"); ok && !e.IsDir() {
			names = append(names, n)
		}
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return names
}

// ErrName is returned for a preset name that can't be a file name.
var ErrName = errors.New("peq: bad preset name")

// CleanName makes s safe as a preset's file name: no separators or control
// characters, trimmed, at most 60 characters.
func CleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ".")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return s
}

func (m *Manager) presetPath(name string) (string, error) {
	n := CleanName(name)
	if n == "" || n != name {
		return "", fmt.Errorf("%w: %q", ErrName, name)
	}
	return filepath.Join(m.dir, presetDir, n+".txt"), nil
}

// LoadPreset plays the preset name.
func (m *Manager) LoadPreset(name string) error {
	path, err := m.presetPath(name)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	p, _, err := ParseAPO(f)
	f.Close()
	if err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	p.Name = name
	m.cur, m.st.Preset = p, name
	m.mu.Unlock()
	m.apply()
	m.saveSoon()
	return nil
}

// SavePreset saves the profile playing as the preset name, replacing one of
// that name, and marks the profile as coming from it.
func (m *Manager) SavePreset(name string) error {
	path, err := m.presetPath(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	p := m.cur.Clone()
	m.mu.Unlock()
	p.Name = name
	if err := writeAtomic(path, []byte(FormatAPO(p))); err != nil {
		return err
	}
	m.mu.Lock()
	m.st.Preset = name
	m.mu.Unlock()
	m.saveSoon()
	return nil
}

// NewPresetName is the first "Preset n" not yet taken.
func (m *Manager) NewPresetName() string {
	have := m.Presets()
	for i := 1; ; i++ {
		n := fmt.Sprintf("Preset %d", i)
		if !slices.Contains(have, n) {
			return n
		}
	}
}

// DeletePreset removes the preset name. The profile playing stays.
func (m *Manager) DeletePreset(name string) error {
	path, err := m.presetPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	m.mu.Lock()
	if m.st.Preset == name {
		m.st.Preset = ""
	}
	m.mu.Unlock()
	m.saveSoon()
	return nil
}

// Imports lists the .txt files in the import folders, by full path.
func (m *Manager) Imports() []string {
	var out []string
	for _, dir := range m.imports {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".txt") && !strings.HasPrefix(e.Name(), ".") {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	return out
}

// Import reads the Equalizer APO file at path, saves it as a preset named
// after the file (without "ParametricEQ"), and plays it. It returns the
// preset's name and what of the file couldn't be applied. A file with no
// filters and no preamp is refused.
func (m *Manager) Import(path string) (string, []Warning, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	p, warns, err := ParseAPO(f)
	f.Close()
	if err != nil {
		return "", warns, err
	}
	if len(p.Filters) == 0 && p.Preamp == 0 {
		return "", warns, fmt.Errorf("%w: no filters found in %s", ErrInvalid, filepath.Base(path))
	}
	if err := p.Validate(); err != nil {
		return "", warns, err
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	// AutoEq names its files "<model> ParametricEQ.txt", squiglink
	// "<model> Filters.txt": the preset is the model.
	for _, suffix := range []string{"ParametricEQ", "Filters"} {
		name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), suffix))
	}
	name = CleanName(name)
	if name == "" {
		name = m.NewPresetName()
	}
	m.mu.Lock()
	p.Name = name
	m.cur, m.st.Preset = p, ""
	m.mu.Unlock()
	if err := m.SavePreset(name); err != nil {
		return "", warns, err
	}
	m.apply()
	return name, warns, nil
}

// writeAtomic writes b to path by way of a temporary file in the same folder,
// synced and renamed over it, so a power cut leaves the old file or the new,
// never half of one.
func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
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
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// SetOutput makes the profile playing the one kept for the output key - a
// Bluetooth headset, a DAC, the speaker - so each has its own EQ. The one
// playing is kept for the output it was on, and key's comes in: the profile it
// had, or, new, a flat one. The first output named takes the profile already
// playing as its own. Each change is saved as before, to the profile playing;
// it is kept for its output at the next switch. A key that can't be a file
// name is ignored.
func (m *Manager) SetOutput(key string) error {
	key = CleanName(key)
	if key == "" {
		return nil
	}
	m.mu.Lock()
	old := m.st.Output
	if old == key {
		m.mu.Unlock()
		return nil
	}
	cur, st := m.cur.Clone(), m.st
	m.mu.Unlock()
	var err error
	if old != "" {
		err = m.keepFor(old, cur, st)
	}
	p, nst, ok := m.keptFor(key)
	switch {
	case ok:
	case old == "":
		p, nst = cur, st // the first output: the EQ it had
	default:
		p, nst = Default(), state{}
	}
	nst.Output = key
	m.mu.Lock()
	m.cur, m.st = p, nst
	m.mu.Unlock()
	m.apply()
	m.saveSoon()
	return err
}

// Output is the output the profile playing is kept for, "" before any.
func (m *Manager) Output() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st.Output
}

func (m *Manager) keepFor(key string, p Profile, st state) error {
	dir := filepath.Join(m.dir, outputDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p.Name = st.Preset
	if err := writeAtomic(filepath.Join(dir, key+".txt"), []byte(FormatAPO(p))); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	return writeAtomic(filepath.Join(dir, key+".json"), b)
}

func (m *Manager) keptFor(key string) (Profile, state, bool) {
	dir := filepath.Join(m.dir, outputDir)
	f, err := os.Open(filepath.Join(dir, key+".txt"))
	if err != nil {
		return Profile{}, state{}, false
	}
	p, _, err := ParseAPO(f)
	f.Close()
	if err != nil || p.Validate() != nil {
		return Profile{}, state{}, false
	}
	var st state
	if b, err := os.ReadFile(filepath.Join(dir, key+".json")); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	p.Name = st.Preset
	return p, st, true
}
