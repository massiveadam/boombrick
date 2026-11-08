package peq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// squigExport is laid out exactly as squiglink's "Export" button writes it
// (squiglink/lab graphtool.js): CRLF lines, the preamp to one decimal, the
// frequency whole, the gain to one decimal, Q to three, shelves as LSC and
// HSC. The values are made up for the test, not a correction for any
// headphone.
const squigExport = "Preamp: -4.3 dB\r\n" +
	"Filter 1: ON LSC Fc 105 Hz Gain 3.5 dB Q 0.707\r\n" +
	"Filter 2: ON PK Fc 2400 Hz Gain -2.1 dB Q 1.800\r\n" +
	"Filter 3: OFF PK Fc 6000 Hz Gain 1.5 dB Q 3.000\r\n" +
	"Filter 4: ON HSC Fc 10000 Hz Gain -1.0 dB Q 0.707\r\n"

func TestParseSquiglinkExport(t *testing.T) {
	p, warns, err := ParseAPO(strings.NewReader(squigExport))
	if err != nil || len(warns) != 0 {
		t.Fatalf("err %v warns %v", err, warns)
	}
	want := []Filter{
		{On: true, Kind: LowShelf, Freq: 105, Gain: 3.5, Q: 0.707},
		{On: true, Kind: Peak, Freq: 2400, Gain: -2.1, Q: 1.8},
		{On: false, Kind: Peak, Freq: 6000, Gain: 1.5, Q: 3},
		{On: true, Kind: HighShelf, Freq: 10000, Gain: -1, Q: 0.707},
	}
	if p.Preamp != -4.3 || len(p.Filters) != len(want) {
		t.Fatalf("%+v", p)
	}
	for i := range want {
		if p.Filters[i] != want[i] {
			t.Errorf("filter %d: %+v, want %+v", i+1, p.Filters[i], want[i])
		}
	}
}

// squiglink's own shelf names, as a hand-edited file may carry them.
func TestParseSquiglinkShelfNames(t *testing.T) {
	p, warns, err := ParseAPO(strings.NewReader("Filter 1: ON LSQ Fc 80 Hz Gain 2 dB Q 0.7\nFilter 2: ON HSQ Fc 9000 Hz Gain -1 dB Q 0.7\n"))
	if err != nil || len(warns) != 0 || len(p.Filters) != 2 || p.Filters[0].Kind != LowShelf || p.Filters[1].Kind != HighShelf {
		t.Fatalf("%+v %v %v", p, warns, err)
	}
}

// squiglink's file is named "<model> Filters.txt"; the preset is the model.
func TestImportSquiglinkNamesThePresetAfterTheModel(t *testing.T) {
	imp := t.TempDir()
	path := filepath.Join(imp, "AirPods Pro 2 Filters.txt")
	os.WriteFile(path, []byte(squigExport), 0o644)
	m, _ := Open(t.TempDir(), NewProcessor(), imp)
	name, warns, err := m.Import(path)
	if err != nil || len(warns) != 0 || name != "AirPods Pro 2" {
		t.Fatalf("%q %v %v", name, warns, err)
	}
	if got := m.Profile(); got.Preamp != -4.3 || len(got.Filters) != 4 {
		t.Fatalf("%+v", got)
	}
}
