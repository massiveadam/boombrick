// Package capture is an audio.Output that records everything played to a
// 16-bit stereo WAV file: a desktop test tool that shows exactly what the
// sound card is sent, EQ included. It passes the audio on to another output,
// or, with none, keeps real time itself as a device would, holding up to
// 100 ms. It is a mock of a device, not one: nothing is heard without the
// output it passes to.
package capture

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/ujjwal313/boombrick/internal/audio"
)

const held = 100 * time.Millisecond

// Output records to a file.
type Output struct {
	mu     sync.Mutex
	inner  audio.Output // nil: keep time alone
	f      *os.File
	rate   int
	frames int64 // written to the file
	// Pacing without an inner output: when the device's clock started and
	// how many frames it has been sent since.
	start      time.Time
	sent       int64
	lastHeader time.Time
}

var _ audio.Output = (*Output)(nil)

// New records to path, replacing it, and passes the audio to inner, which may
// be nil.
func New(path string, inner audio.Output) (*Output, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	o := &Output{inner: inner, f: f, rate: audio.OutputRate}
	if err := o.header(); err != nil {
		f.Close()
		return nil, err
	}
	return o, nil
}

// header writes the WAV header for the frames so far.
func (o *Output) header() error {
	data := uint32(o.frames * 4)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 2)
	binary.LittleEndian.PutUint32(h[24:], uint32(o.rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(o.rate*4))
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	_, err := o.f.WriteAt(h, 0)
	return err
}

// Open opens the inner output at rate. A capture holds one rate.
func (o *Output) Open(rate int) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.f == nil {
		return errors.New("capture: closed")
	}
	if rate != o.rate {
		return errors.New("capture: the rate can't change")
	}
	if o.inner != nil {
		return o.inner.Open(rate)
	}
	return nil
}

// Write records buf and plays it on, or waits as a device would.
func (o *Output) Write(buf []int16) error {
	o.mu.Lock()
	if o.f == nil {
		o.mu.Unlock()
		return errors.New("capture: closed")
	}
	b := make([]byte, 2*len(buf))
	for i, v := range buf {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	_, err := o.f.WriteAt(b, 44+o.frames*4)
	o.frames += int64(len(buf) / 2)
	if now := time.Now(); now.Sub(o.lastHeader) > time.Second {
		o.lastHeader = now
		_ = o.header() // the file stays playable if the program is killed
	}
	inner := o.inner
	var wait time.Duration
	if inner == nil {
		now := time.Now()
		if o.start.IsZero() || o.queued(now) == 0 {
			o.start, o.sent = now, 0 // an underrun: the clock starts again
		}
		o.sent += int64(len(buf) / 2)
		wait = o.queuedTime(now) - held
	}
	o.mu.Unlock()
	if err != nil {
		return err
	}
	if inner != nil {
		return inner.Write(buf)
	}
	if wait > 0 {
		time.Sleep(wait)
	}
	return nil
}

func (o *Output) queuedTime(now time.Time) time.Duration {
	return time.Duration(o.queued(now)) * time.Second / time.Duration(o.rate)
}

// queued is how many frames sent haven't played by now.
func (o *Output) queued(now time.Time) int64 {
	played := int64(now.Sub(o.start)) * int64(o.rate) / int64(time.Second)
	return max(o.sent-played, 0)
}

// Drop drops what the inner output holds, or what the clock says is queued.
func (o *Output) Drop() (int, error) {
	o.mu.Lock()
	inner := o.inner
	n := 0
	if inner == nil && !o.start.IsZero() {
		n = int(o.queued(time.Now()))
		o.start, o.sent = time.Time{}, 0
	}
	o.mu.Unlock()
	if inner != nil {
		return inner.Drop()
	}
	return n, nil
}

// Close closes the inner output; the file stays open for the next Open.
// Finish ends the recording.
func (o *Output) Close() error {
	o.mu.Lock()
	inner := o.inner
	o.start, o.sent = time.Time{}, 0
	o.mu.Unlock()
	if inner != nil {
		return inner.Close()
	}
	return nil
}

// Finish writes the header and closes the file.
func (o *Output) Finish() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.f == nil {
		return nil
	}
	err := o.header()
	if cerr := o.f.Close(); err == nil {
		err = cerr
	}
	o.f = nil
	return err
}

// Frames is how many frames have been recorded.
func (o *Output) Frames() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.frames
}
