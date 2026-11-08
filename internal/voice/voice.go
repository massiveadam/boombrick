// Package voice is push-to-talk for a terminal on the handheld: the device
// records a short clip and sends it to a server, which transcribes it and
// types the text into a tmux session there, where a coding agent runs.
//
// The text is typed, never entered: newlines and other control characters
// are taken out, so a misheard word can't run anything. Pressing Enter is a
// separate request, after the text has been read on the screen. Only a few
// named keys can be sent that way.
package voice

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Transcriber turns a WAV clip into text.
type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte) (string, error)
}

// OpenAI transcribes through an OpenAI-compatible endpoint
// (POST .../v1/audio/transcriptions), as speaches (faster-whisper) and
// whisper.cpp's server offer.
type OpenAI struct {
	URL      string // the full transcriptions URL
	Model    string
	Language string // "" to detect
	Key      string // "" for none
	Client   *http.Client
}

// Transcribe posts the clip and returns its text.
func (o OpenAI) Transcribe(ctx context.Context, wav []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "clip.wav")
	if err != nil {
		return "", err
	}
	fw.Write(wav)
	if o.Model != "" {
		mw.WriteField("model", o.Model)
	}
	if o.Language != "" {
		mw.WriteField("language", o.Language)
	}
	mw.WriteField("response_format", "json")
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if o.Key != "" {
		req.Header.Set("Authorization", "Bearer "+o.Key)
	}
	c := o.Client
	if c == nil {
		c = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("voice: transcriber: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("voice: transcriber: %w", err)
	}
	return r.Text, nil
}

// Typer puts text and keys into the session the agent runs in.
type Typer interface {
	Type(text string) error
	Key(name string) error
}

// Tmux types into a tmux pane with send-keys.
type Tmux struct {
	Target string // a session, window or pane: "pocket", "pocket:0.1"
	Bin    string // "" for "tmux"
}

func (t Tmux) bin() string {
	if t.Bin == "" {
		return "tmux"
	}
	return t.Bin
}

// Type sends text literally (-l), so nothing in it is read as a key name.
func (t Tmux) Type(text string) error {
	out, err := exec.Command(t.bin(), "send-keys", "-t", t.Target, "-l", "--", text).CombinedOutput()
	if err != nil {
		return fmt.Errorf("voice: tmux: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Key sends one named key.
func (t Tmux) Key(name string) error {
	out, err := exec.Command(t.bin(), "send-keys", "-t", t.Target, name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("voice: tmux: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Keys are the keys a client may send: Enter after reading the text, and
// the few needed to correct it or back out.
var Keys = map[string]string{
	"enter":     "Enter",
	"escape":    "Escape",
	"backspace": "BSpace",
	"clear":     "C-u", // the line so far, in a shell and in most prompts
	"up":        "Up",
	"down":      "Down",
	"tab":       "Tab",
}

// Clean makes a transcript safe to type: every control character, newlines
// among them, becomes a space, runs of spaces become one, and the ends are
// trimmed. Whisper's usual hallucinations on silence come out as "".
func Clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 || r == 0xFEFF {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	switch strings.ToLower(strings.Trim(s, " .!")) {
	case "", "you", "thank you", "thanks for watching", "[blank_audio]", "(silence)":
		return ""
	}
	return s
}

// MaxClip is the largest clip accepted: about 5 minutes of 16 kHz mono.
const MaxClip = 10 << 20

// Server answers the handheld.
//
//	POST /v1/dictate         a WAV body; types the text (?type=0 to only return it)
//	POST /v1/key/<name>      one of Keys
//	GET  /v1/health
//
// Every request needs "Authorization: Bearer <Token>".
type Server struct {
	STT   Transcriber
	Typer Typer
	Token string
	Log   *slog.Logger

	mu      sync.Mutex
	pending bool // text typed since the last Enter or clear: the next is spaced from it
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return s.Log
}

// ErrNoToken is returned by Check for a server without a token.
var ErrNoToken = errors.New("voice: a token is needed")

// Check reports whether the server is safe to start.
func (s *Server) Check() error {
	if len(s.Token) < 16 {
		return ErrNoToken
	}
	if s.STT == nil || s.Typer == nil {
		return errors.New("voice: a transcriber and a typer are needed")
	}
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if s.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/v1/health" && r.Method == http.MethodGet:
		reply(w, http.StatusOK, map[string]string{"status": "ok"})
	case r.URL.Path == "/v1/dictate" && r.Method == http.MethodPost:
		s.dictate(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/key/") && r.Method == http.MethodPost:
		key, ok := Keys[strings.TrimPrefix(r.URL.Path, "/v1/key/")]
		if !ok {
			http.Error(w, "unknown key", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		err := s.Typer.Key(key)
		if err == nil && (key == "Enter" || key == "C-u" || key == "Escape") {
			s.pending = false
		}
		s.mu.Unlock()
		if err != nil {
			s.log().Warn("voice key", "err", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		reply(w, http.StatusOK, map[string]string{"key": key})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) dictate(w http.ResponseWriter, r *http.Request) {
	wav, err := io.ReadAll(io.LimitReader(r.Body, MaxClip+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(wav) > MaxClip {
		http.Error(w, "clip too long", http.StatusRequestEntityTooLarge)
		return
	}
	if len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		http.Error(w, "not a WAV file", http.StatusBadRequest)
		return
	}
	start := time.Now()
	raw, err := s.STT.Transcribe(r.Context(), wav)
	if err != nil {
		s.log().Warn("voice transcribe", "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	text := Clean(raw)
	typed := false
	if text != "" && r.URL.Query().Get("type") != "0" {
		s.mu.Lock()
		out := text
		if s.pending {
			out = " " + text
		}
		err := s.Typer.Type(out)
		if err == nil {
			s.pending = true
		}
		s.mu.Unlock()
		if err != nil {
			s.log().Warn("voice type", "err", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		typed = true
	}
	s.log().Info("voice dictate", "bytes", len(wav), "chars", len(text), "typed", typed, "took", time.Since(start).Round(time.Millisecond))
	reply(w, http.StatusOK, map[string]any{"text": text, "typed": typed})
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
