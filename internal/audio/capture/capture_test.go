package capture

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordsAWav(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	o, err := New(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Open(48000); err != nil {
		t.Fatal(err)
	}
	if o.Open(44100) == nil {
		t.Fatal("rate change accepted")
	}
	buf := make([]int16, 2*4800) // 100 ms
	for i := range buf {
		buf[i] = int16(i)
	}
	start := time.Now()
	for range 3 {
		if err := o.Write(buf); err != nil {
			t.Fatal(err)
		}
	}
	// 300 ms sent with 100 ms held: about 200 ms of waiting.
	if d := time.Since(start); d < 150*time.Millisecond || d > 600*time.Millisecond {
		t.Errorf("writing 300 ms took %v", d)
	}
	if err := o.Finish(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if len(b) != 44+3*len(buf)*2 || string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " {
		t.Fatalf("file %d bytes", len(b))
	}
	if got := binary.LittleEndian.Uint32(b[40:]); got != uint32(3*len(buf)*2) {
		t.Fatalf("data size %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(b[44+2*5:])); got != 5 {
		t.Fatalf("sample 5 = %d", got)
	}
}
