package platform

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ujjwal313/boombrick/internal/audio/route"
)

// AudioState reads what plays where on the RG SP, as plorpOS's launcher reads
// it (src/main.c aout_bt_sink and aout_usb_sink, src/platform_h700.c), from
// the root of the file system, normally "/":
//
//   - the Bluetooth headset's BlueALSA device: the first line of
//     /tmp/tortos_btsink, which plorpOS's radio.sh writes while one is
//     connected and ready to play;
//   - a USB DAC: any card snd-usb-audio made, from /proc/asound/cards;
//   - a wired plug: the PMIC's speaker state, "0" while a plug is in (the
//     jack's own extcon never moves on the RG SP, plorpOS found).
func AudioState(root string) route.State {
	st := route.State{Wired: readLine(filepath.Join(root, "sys/class/power_supply/axp2202-battery"), "spk_state") == "0"}
	if b, err := os.ReadFile(filepath.Join(root, "tmp/tortos_btsink")); err == nil {
		st.Bluetooth = strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	}
	if b, err := os.ReadFile(filepath.Join(root, "proc/asound/cards")); err == nil {
		st.USB = usbCard(string(b))
	}
	return st
}

// usbCard is the ALSA name of the first USB audio card in /proc/asound/cards,
// whose card lines read " 3 [KA13           ]: USB-Audio - FIIO KA13".
func usbCard(cards string) string {
	sc := bufio.NewScanner(strings.NewReader(cards))
	for sc.Scan() {
		line := sc.Text()
		if len(line) < 2 || line[0] != ' ' && (line[0] < '0' || line[0] > '9') {
			continue
		}
		head := strings.TrimLeft(line, " ")
		if len(line)-len(head) > 2 {
			continue // a description line, indented past the index
		}
		i := strings.IndexFunc(head, func(r rune) bool { return r < '0' || r > '9' })
		if i <= 0 {
			continue
		}
		if _, err := strconv.Atoi(head[:i]); err != nil {
			continue
		}
		rest := strings.TrimLeft(head[i:], " ")
		if !strings.HasPrefix(rest, "[") {
			continue
		}
		end := strings.Index(rest, "]")
		if end < 0 {
			continue
		}
		id := strings.TrimRight(rest[1:end], " ")
		if id == "" || !strings.HasPrefix(rest[end+1:], ": USB-Audio") {
			continue
		}
		return "plughw:CARD=" + id + ",DEV=0"
	}
	return ""
}
