// Package alsa writes audio to the default ALSA device, which NextUI points at
// the speaker, headphones, a USB DAC or Bluetooth.
package alsa

/*
#cgo LDFLAGS: -lasound
#include <alsa/asoundlib.h>
#include <errno.h>
#include <stdlib.h>

// The device keeps latency_us queued, enough to ride out scheduling hiccups.
static int bb_open(snd_pcm_t **pcm, const char *name, unsigned int rate, unsigned int latency_us) {
	int err = snd_pcm_open(pcm, name, SND_PCM_STREAM_PLAYBACK, 0);
	if (err < 0) return err;
	err = snd_pcm_set_params(*pcm, SND_PCM_FORMAT_S16_LE, SND_PCM_ACCESS_RW_INTERLEAVED, 2, rate, 1, latency_us);
	if (err < 0) {
		snd_pcm_close(*pcm);
		*pcm = NULL;
	}
	return err;
}

// A device that takes nothing for stall_ms is stalled: -ETIMEDOUT, rather than
// waiting on it for ever. BlueALSA's can stop after an underrun and never
// start again until it is closed and opened.
static int bb_write(snd_pcm_t *pcm, const short *buf, snd_pcm_uframes_t frames, int stall_ms) {
	while (frames > 0) {
		int w = snd_pcm_wait(pcm, stall_ms);
		if (w == 0) return -ETIMEDOUT;
		if (w < 0) {
			int err = snd_pcm_recover(pcm, w, 1);
			if (err < 0) return err;
			continue;
		}
		snd_pcm_sframes_t n = snd_pcm_writei(pcm, buf, frames);
		if (n < 0) {
			int err = snd_pcm_recover(pcm, (int)n, 1);
			if (err < 0) return err;
			continue;
		}
		buf += n * 2;
		frames -= n;
	}
	return 0;
}

static long bb_drop(snd_pcm_t *pcm) {
	snd_pcm_sframes_t delay = 0;
	if (snd_pcm_delay(pcm, &delay) < 0 || delay < 0) delay = 0;
	int err = snd_pcm_drop(pcm);
	if (err < 0) return err;
	err = snd_pcm_prepare(pcm);
	if (err < 0) return err;
	return delay;
}

static long bb_queued(snd_pcm_t *pcm) {
	snd_pcm_sframes_t delay;
	if (snd_pcm_delay(pcm, &delay) < 0 || delay < 0) return 0;
	return delay;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/ujjwal313/boombrick/internal/audio"
)

// Device is a playback device: "default", or another ALSA name.
type Device struct {
	pcm     *C.snd_pcm_t
	rate    int
	name    string
	latency time.Duration // queued in the device; 100 ms unless set
}

// defaultLatency is what a device keeps queued unless told otherwise.
const defaultLatency = 100 * time.Millisecond

// stallAfter is how long a device may take nothing before Write gives up on
// it as stalled.
const stallAfter = 2 * time.Second

var _ audio.Output = (*Device)(nil)

// New returns the default device, which opens on first use.
func New() *Device { return &Device{name: "default", latency: defaultLatency} }

// NewNamed returns the device ALSA knows as name, such as a BlueALSA PCM or
// "plughw:CARD=KA13,DEV=0", which opens on first use.
func NewNamed(name string) *Device { return &Device{name: name, latency: defaultLatency} }

// NewBuffered is NewNamed keeping latency queued: more for a Bluetooth
// headset, whose link drifts, than for the sound card.
func NewBuffered(name string, latency time.Duration) *Device {
	return &Device{name: name, latency: latency}
}

// Name is the ALSA name the device opens.
func (d *Device) Name() string { return d.name }

// Error is an ALSA failure; Busy reports whether another program holds the
// device, as on a card without dmix while a game plays.
type Error struct {
	Op   string
	Code int // negative errno, as ALSA returns
}

func (e *Error) Error() string {
	return fmt.Sprintf("alsa %s: %s", e.Op, C.GoString(C.snd_strerror(C.int(e.Code))))
}

// Busy reports whether the device was held by someone else.
func (e *Error) Busy() bool { return e.Code == -int(syscall.EBUSY) }

// Stalled reports whether the device stopped taking audio: closed by Write,
// it plays again once opened afresh.
func (e *Error) Stalled() bool { return e.Code == -int(syscall.ETIMEDOUT) }

func alsaErr(op string, code C.int) error { return &Error{Op: op, Code: int(code)} }

// Open opens the device at rate. If it is already open at another rate, the
// queued audio is played out first so the previous track isn't cut short.
func (d *Device) Open(rate int) error {
	if d.pcm != nil && d.rate == rate {
		return nil
	}
	if d.pcm != nil {
		C.snd_pcm_drain(d.pcm)
		d.Close()
	}
	var pcm *C.snd_pcm_t
	name := C.CString(d.name)
	defer C.free(unsafe.Pointer(name))
	if err := C.bb_open(&pcm, name, C.uint(rate), C.uint(d.latency/time.Microsecond)); err < 0 {
		return alsaErr("open", err)
	}
	d.pcm, d.rate = pcm, rate
	return nil
}

// Write plays buf, recovering from underruns.
func (d *Device) Write(buf []int16) error {
	if d.pcm == nil {
		return errors.New("alsa: write to a closed device")
	}
	if len(buf) < 2 {
		return nil
	}
	if err := C.bb_write(d.pcm, (*C.short)(unsafe.Pointer(&buf[0])), C.snd_pcm_uframes_t(len(buf)/2), C.int(stallAfter/time.Millisecond)); err < 0 {
		// The device is gone or unrecoverable; close it so the next Open
		// starts afresh.
		d.Close()
		return alsaErr("write", err)
	}
	return nil
}

// Drop discards queued audio, leaves the device ready for more and reports how
// many frames it held.
func (d *Device) Drop() (int, error) {
	if d.pcm == nil {
		return 0, nil
	}
	n := C.bb_drop(d.pcm)
	if n < 0 {
		return 0, alsaErr("drop", C.int(n))
	}
	return int(n), nil
}

// queued reports the frames waiting in the device; the tests use it to check Drop.
func (d *Device) queued() int {
	if d.pcm == nil {
		return 0
	}
	return int(C.bb_queued(d.pcm))
}

// Close closes the device; queued audio is discarded.
func (d *Device) Close() error {
	if d.pcm == nil {
		return nil
	}
	err := C.snd_pcm_close(d.pcm)
	d.pcm = nil
	if err < 0 {
		return alsaErr("close", err)
	}
	return nil
}
