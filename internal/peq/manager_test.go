package peq

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestManagerStartsWithDefault(t *testing.T) {
	dir := t.TempDir()
	proc := NewProcessor()
	m, err := Open(dir, proc)
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Profile(); len(p.Filters) != 10 || !p.Flat() {
		t.Fatalf("default %+v", p)
	}
	if m.Bypassed() || m.Name() != "Custom" {
		t.Fatalf("bypassed %v name %q", m.Bypassed(), m.Name())
	}
}

// What one Manager saves, the next reads: the foreground player's EQ is the
// background player's.
func TestManagerPersists(t *testing.T) {
	dir := t.TempDir()
	m, _ := Open(dir, NewProcessor())
	p := autoEqLike.Clone()
	if err := m.SetProfile(p); err != nil {
		t.Fatal(err)
	}
	m.SetBypass(true)
	if err := m.Flush(); err != nil {
		t.Fatal(err)
	}
	proc := NewProcessor()
	n, err := Open(dir, proc)
	if err != nil {
		t.Fatal(err)
	}
	got := n.Profile()
	if got.Preamp != p.Preamp || !slices.Equal(got.Filters, p.Filters) || !n.Bypassed() {
		t.Fatalf("reopened %+v bypass %v", got, n.Bypassed())
	}
	// And it is what the processor plays.
	buf := sine(4800, 48000, 1000, 1000, 0.5)
	orig := append([]float32(nil), buf...)
	proc.Process(buf, 48000)
	if !slices.Equal(buf, orig) {
		t.Fatal("bypassed EQ changed the audio")
	}
}

func TestManagerRefusesInvalid(t *testing.T) {
	m, _ := Open(t.TempDir(), NewProcessor())
	if err := m.SetProfile(Profile{Preamp: 99}); err == nil {
		t.Fatal("invalid profile accepted")
	}
}

func TestManagerPresets(t *testing.T) {
	dir := t.TempDir()
	m, _ := Open(dir, NewProcessor())
	_ = m.SetProfile(autoEqLike)
	if err := m.SavePreset("My HD650"); err != nil {
		t.Fatal(err)
	}
	if m.Name() != "My HD650" {
		t.Fatalf("name %q", m.Name())
	}
	_ = m.SetProfile(Default())
	if m.Name() != "Custom" {
		t.Fatalf("after a change, name %q", m.Name())
	}
	if err := m.LoadPreset("My HD650"); err != nil {
		t.Fatal(err)
	}
	if got := m.Profile(); !slices.Equal(got.Filters, autoEqLike.Filters) || got.Preamp != autoEqLike.Preamp {
		t.Fatalf("loaded %+v", got)
	}
	if n := m.NewPresetName(); n != "Preset 1" {
		t.Fatalf("new name %q", n)
	}
	for _, bad := range []string{"", "../x", "a/b", ".."} {
		if err := m.SavePreset(bad); err == nil {
			t.Errorf("saved preset named %q", bad)
		}
	}
	if err := m.DeletePreset("My HD650"); err != nil {
		t.Fatal(err)
	}
	if len(m.Presets()) != 0 || m.Name() != "Custom" {
		t.Fatalf("after delete: %v %q", m.Presets(), m.Name())
	}
}

func TestManagerImport(t *testing.T) {
	data, imp := t.TempDir(), t.TempDir()
	path := filepath.Join(imp, "Sennheiser HD 650 ParametricEQ.txt")
	if err := os.WriteFile(path, []byte(autoEqText), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(imp, "notes.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(imp, "empty.txt"), []byte("# nothing\n"), 0o644)
	m, _ := Open(data, NewProcessor(), imp, filepath.Join(imp, "missing"))
	if got := m.Imports(); len(got) != 2 {
		t.Fatalf("imports %v", got)
	}
	name, warns, err := m.Import(path)
	if err != nil || len(warns) != 0 {
		t.Fatalf("import: %v %v", err, warns)
	}
	if name != "Sennheiser HD 650" || m.Name() != name || !slices.Contains(m.Presets(), name) {
		t.Fatalf("name %q, current %q, presets %v", name, m.Name(), m.Presets())
	}
	if _, _, err := m.Import(filepath.Join(imp, "empty.txt")); err == nil {
		t.Fatal("empty preset imported")
	}
	// The source file is only read.
	if b, _ := os.ReadFile(path); string(b) != autoEqText {
		t.Fatal("import changed its source")
	}
}

func TestDamagedFilesFallBack(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, currentFile), []byte("Preamp: 99 dB\n"), 0o644)
	os.WriteFile(filepath.Join(dir, stateFile), []byte("{not json"), 0o644)
	m, err := Open(dir, NewProcessor())
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Profile(); len(p.Filters) != 10 || m.Bypassed() {
		t.Fatalf("fallback %+v", p)
	}
}
