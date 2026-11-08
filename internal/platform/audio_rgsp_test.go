package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ujjwal313/boombrick/internal/audio/route"
)

const cardsCodecOnly = ` 0 [audiocodec     ]: audiocodec - audiocodec
                      audiocodec
`

const cardsWithDAC = cardsCodecOnly + ` 3 [KA13           ]: USB-Audio - FIIO KA13
                      FiiO FIIO KA13 at usb-sunxi-ehci-1, high speed
`

func fakeRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAudioState(t *testing.T) {
	spk := "sys/class/power_supply/axp2202-battery/spk_state"
	cases := []struct {
		files map[string]string
		want  route.State
		sink  route.Kind
	}{
		{map[string]string{spk: "1\n", "proc/asound/cards": cardsCodecOnly}, route.State{}, route.Speaker},
		{map[string]string{spk: "0\n"}, route.State{Wired: true}, route.Wired},
		{map[string]string{spk: "1\n", "tmp/tortos_btsink": "bluealsa:DEV=AA:BB:CC:DD:EE:FF,PROFILE=a2dp\n/org/bluealsa/hci0/dev_AA/a2dpsrc/sink\n"},
			route.State{Bluetooth: "bluealsa:DEV=AA:BB:CC:DD:EE:FF,PROFILE=a2dp"}, route.Bluetooth},
		{map[string]string{spk: "1\n", "proc/asound/cards": cardsWithDAC, "tmp/tortos_btsink": "bt\n"},
			route.State{USB: "plughw:CARD=KA13,DEV=0", Bluetooth: "bt"}, route.USB},
		{map[string]string{}, route.State{}, route.Speaker}, // nothing readable: the speaker
	}
	for i, c := range cases {
		got := AudioState(fakeRoot(t, c.files))
		if got != c.want || route.Resolve(got).Kind != c.sink {
			t.Errorf("case %d: %+v -> %v, want %+v -> %v", i, got, route.Resolve(got).Kind, c.want, c.sink)
		}
	}
}

func TestUSBCardIgnoresDescriptions(t *testing.T) {
	// A description line mentioning USB-Audio isn't a card line.
	if got := usbCard(" 0 [codec ]: audiocodec - x\n                      USB-Audio thing\n"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := usbCard("10 [Dongle         ]: USB-Audio - Apple USB-C\n"); got != "plughw:CARD=Dongle,DEV=0" {
		t.Fatalf("two-digit index: %q", got)
	}
}
