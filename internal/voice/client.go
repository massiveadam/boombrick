package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Client talks to a Server.
type Client struct {
	Base  string // "http://sanchez:8178"
	Token string
	HTTP  *http.Client // nil: one that uses $HTTP_PROXY (Tailscale's, in userspace mode)
}

func (c Client) do(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.Base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voice: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// Dictate sends a WAV clip, which the server transcribes and types.
func (c Client) Dictate(ctx context.Context, wav []byte) (string, error) {
	b, err := c.do(ctx, "/v1/dictate", wav)
	if err != nil {
		return "", err
	}
	var r struct {
		Text string `json:"text"`
	}
	return r.Text, json.Unmarshal(b, &r)
}

// Key sends one of Keys by its name ("enter").
func (c Client) Key(ctx context.Context, name string) error {
	_, err := c.do(ctx, "/v1/key/"+name, nil)
	return err
}

// Recorder records a clip while a button is held. Arecord does the
// recording: it is in alsa-utils, and stops cleanly, its WAV header written,
// on SIGINT.
type Recorder struct {
	Device string // ALSA device; "" for "default"
	Bin    string // "" for "arecord"
	Dir    string // where clips are kept while they're sent; "" for os.TempDir()

	cmd  *exec.Cmd
	path string
	from time.Time
}

// Start begins a clip: 16 kHz mono, what the transcriber works at.
func (r *Recorder) Start() error {
	if r.cmd != nil {
		return errors.New("voice: already recording")
	}
	dir := r.Dir
	if dir == "" {
		dir = os.TempDir()
	}
	r.path = filepath.Join(dir, fmt.Sprintf("ptt-%d.wav", time.Now().UnixNano()))
	dev, bin := r.Device, r.Bin
	if dev == "" {
		dev = "default"
	}
	if bin == "" {
		bin = "arecord"
	}
	r.cmd = exec.Command(bin, "-q", "-D", dev, "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "wav", r.path)
	r.from = time.Now()
	if err := r.cmd.Start(); err != nil {
		r.cmd = nil
		return err
	}
	return nil
}

// MinClip is the shortest clip sent: shorter is a button tapped by mistake.
const MinClip = 400 * time.Millisecond

// Stop ends the clip and returns it, or nil for one too short to send.
func (r *Recorder) Stop() ([]byte, error) {
	if r.cmd == nil {
		return nil, errors.New("voice: not recording")
	}
	cmd, path, held := r.cmd, r.path, time.Since(r.from)
	r.cmd = nil
	defer os.Remove(path)
	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	if held < MinClip {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) <= 44 {
		return nil, errors.New("voice: nothing recorded (is there a microphone?)")
	}
	return b, nil
}

// Event is a key event read from a Linux input device.
type Event struct {
	Code  uint16
	Value int32 // 1 pressed, 0 released, 2 repeat
}

// evKey is the input event type for keys and buttons.
const evKey = 1

// ReadKeys reads key events from an input device (/dev/input/eventN) until
// it fails or ctx ends, sending each to out. It doesn't grab the device:
// the launcher and the terminal still see every button.
func ReadKeys(ctx context.Context, path string, out chan<- Event) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); f.Close() }()
	return readEvents(f, out)
}

// readEvents decodes struct input_event on a 64-bit kernel: a 16-byte time,
// then type, code (uint16) and value (int32).
func readEvents(r io.Reader, out chan<- Event) error {
	buf := make([]byte, 24)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		if binary.LittleEndian.Uint16(buf[16:]) != evKey {
			continue
		}
		out <- Event{Code: binary.LittleEndian.Uint16(buf[18:]), Value: int32(binary.LittleEndian.Uint32(buf[20:]))}
	}
}
