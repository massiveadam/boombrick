package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const token = "0123456789abcdef0123"

type fakeSTT struct{ text string }

func (f fakeSTT) Transcribe(context.Context, []byte) (string, error) { return f.text, nil }

type fakeTyper struct{ typed, keys []string }

func (f *fakeTyper) Type(s string) error { f.typed = append(f.typed, s); return nil }
func (f *fakeTyper) Key(k string) error  { f.keys = append(f.keys, k); return nil }

func wav(n int) []byte {
	b := make([]byte, 44+n)
	copy(b, "RIFF")
	copy(b[8:], "WAVE")
	return b
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"  hello\nworld  ":         "hello world",
		"run tests\r\n\x1b[A\tnow": "run tests [A now",
		"line\u2028sep":            "line sep",
		" Thank you. ":             "",
		"you":                      "",
		"Check the EQ tests, then": "Check the EQ tests, then",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func post(h http.Handler, path, tok string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServerTypesCleanTextAndNeverEnter(t *testing.T) {
	ty := &fakeTyper{}
	s := &Server{STT: fakeSTT{"fix the tests\nrm -rf /"}, Typer: ty, Token: token}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	w := post(s, "/v1/dictate", token, wav(100))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(ty.typed) != 1 || ty.typed[0] != "fix the tests rm -rf /" || len(ty.keys) != 0 {
		t.Fatalf("typed %q keys %q", ty.typed, ty.keys)
	}
	if !strings.Contains(w.Body.String(), `"typed":true`) {
		t.Fatal(w.Body.String())
	}
	// ?type=0 only transcribes.
	post(s, "/v1/dictate?type=0", token, wav(100))
	if len(ty.typed) != 1 {
		t.Fatal("typed with type=0")
	}
	// A second clip before Enter is spaced from the first; after Enter it isn't.
	post(s, "/v1/dictate", token, wav(100))
	post(s, "/v1/key/enter", token, nil)
	post(s, "/v1/dictate", token, wav(100))
	if ty.typed[1] != " fix the tests rm -rf /" || ty.typed[2] != "fix the tests rm -rf /" {
		t.Fatalf("%q", ty.typed)
	}
}

func TestServerRefuses(t *testing.T) {
	ty := &fakeTyper{}
	s := &Server{STT: fakeSTT{"x"}, Typer: ty, Token: token}
	for _, c := range []struct {
		path, tok string
		body      []byte
		code      int
	}{
		{"/v1/dictate", "", wav(10), 401},
		{"/v1/dictate", "wrong-token-wrong-token", wav(10), 401},
		{"/v1/dictate", token, []byte("not a wav file at all, no header here......"), 400},
		{"/v1/dictate", token, wav(MaxClip), 413},
		{"/v1/key/C-c", token, nil, 400},
		{"/v1/key/rm", token, nil, 400},
		{"/v1/other", token, nil, 404},
	} {
		if w := post(s, c.path, c.tok, c.body); w.Code != c.code {
			t.Errorf("%s with %q: %d, want %d", c.path, c.tok, w.Code, c.code)
		}
	}
	if len(ty.typed)+len(ty.keys) != 0 {
		t.Fatalf("typed %q keys %q", ty.typed, ty.keys)
	}
	if (&Server{STT: fakeSTT{}, Typer: ty, Token: "short"}).Check() == nil {
		t.Fatal("a short token was accepted")
	}
}

func TestServerKeys(t *testing.T) {
	ty := &fakeTyper{}
	s := &Server{STT: fakeSTT{}, Typer: ty, Token: token}
	for _, k := range []string{"enter", "clear", "escape"} {
		if w := post(s, "/v1/key/"+k, token, nil); w.Code != 200 {
			t.Fatalf("%s: %d", k, w.Code)
		}
	}
	if strings.Join(ty.keys, ",") != "Enter,C-u,Escape" {
		t.Fatal(ty.keys)
	}
}

func TestOpenAITranscriber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		fields := map[string]string{}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			fields[p.FormName()] = string(b)
		}
		if r.URL.Path != "/v1/audio/transcriptions" || fields["model"] != "small" || fields["language"] != "en" || !strings.HasPrefix(fields["file"], "RIFF") {
			http.Error(w, fmt.Sprint(fields), 400)
			return
		}
		fmt.Fprint(w, `{"text":" hello there"}`)
	}))
	defer srv.Close()
	got, err := OpenAI{URL: srv.URL + "/v1/audio/transcriptions", Model: "small", Language: "en"}.Transcribe(context.Background(), wav(10))
	if err != nil || got != " hello there" {
		t.Fatalf("%q %v", got, err)
	}
}

// TestTmux types into a real tmux session, when tmux is here: the text
// arrives literally and nothing is entered until Enter is sent.
func TestTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	sock := t.TempDir() + "/tmux.sock"
	tmux := func(a ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-S", sock}, a...)...).CombinedOutput()
		return string(out)
	}
	tmux("new-session", "-d", "-s", "t", "-x", "120", "-y", "10", "cat -v")
	defer tmux("kill-server")
	time.Sleep(200 * time.Millisecond)
	ty := Tmux{Target: "t"}
	ty.Bin = writeWrapper(t, sock)
	if err := ty.Type("echo Enter C-c $HOME; done"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	pane := tmux("capture-pane", "-p", "-t", "t")
	if !strings.Contains(pane, "echo Enter C-c $HOME; done") {
		t.Fatalf("pane: %q", pane)
	}
	if err := ty.Key("Enter"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	pane = tmux("capture-pane", "-p", "-t", "t")
	if strings.Count(pane, "echo Enter C-c $HOME; done") != 2 { // the line, and cat's echo of it
		t.Fatalf("after Enter: %q", pane)
	}
}

// TestTmuxBracketedPaste: a program that asks for bracketed paste, as agent
// TUIs do, gets the text between the paste markers, not as keystrokes.
func TestTmuxBracketedPaste(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	sock := t.TempDir() + "/tmux.sock"
	tmux := func(a ...string) string {
		out, _ := exec.Command("tmux", append([]string{"-S", sock}, a...)...).CombinedOutput()
		return string(out)
	}
	tmux("new-session", "-d", "-s", "t", "-x", "120", "-y", "10", `printf '\033[?2004h'; exec cat -v`)
	defer tmux("kill-server")
	time.Sleep(300 * time.Millisecond)
	ty := Tmux{Target: "t", Bin: writeWrapper(t, sock)}
	if err := ty.Type("/help me"); err != nil {
		t.Fatal(err)
	}
	ty.Key("Enter")
	time.Sleep(300 * time.Millisecond)
	if pane := tmux("capture-pane", "-p", "-t", "t"); !strings.Contains(pane, "^[[200~/help me^[[201~") {
		t.Fatalf("pane: %q", pane)
	}
}

// writeWrapper is a tmux that talks to the test's own server.
func writeWrapper(t *testing.T, sock string) string {
	p := t.TempDir() + "/tmux"
	real, _ := exec.LookPath("tmux")
	script := "#!/bin/sh\nexec " + real + " -S " + sock + " \"$@\"\n"
	if err := writeFile(p, script); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadEvents(t *testing.T) {
	var buf bytes.Buffer
	ev := func(typ, code uint16, val int32) {
		b := make([]byte, 24)
		binary.LittleEndian.PutUint16(b[16:], typ)
		binary.LittleEndian.PutUint16(b[18:], code)
		binary.LittleEndian.PutUint32(b[20:], uint32(val))
		buf.Write(b)
	}
	ev(1, 313, 1) // R2 down
	ev(0, 0, 0)   // sync: skipped
	ev(3, 2, 99)  // an axis: skipped
	ev(1, 313, 0) // R2 up
	out := make(chan Event, 8)
	if err := readEvents(&buf, out); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	close(out)
	var got []Event
	for e := range out {
		got = append(got, e)
	}
	if len(got) != 2 || got[0] != (Event{313, 1}) || got[1] != (Event{313, 0}) {
		t.Fatal(got)
	}
}

type fakeRec struct {
	on   bool
	clip []byte
}

func (f *fakeRec) Start() error { f.on = true; return nil }
func (f *fakeRec) Stop() ([]byte, error) {
	f.on = false
	return f.clip, nil
}

type fakeRemote struct{ sent, keys []string }

func (f *fakeRemote) Dictate(_ context.Context, w []byte) (string, error) {
	f.sent = append(f.sent, string(w))
	return "hello", nil
}
func (f *fakeRemote) Key(_ context.Context, k string) error { f.keys = append(f.keys, k); return nil }

func TestPTT(t *testing.T) {
	b := Buttons{Talk: 313, Enter: 311, Clear: 310}
	rec := &fakeRec{clip: []byte("clip")}
	rem := &fakeRemote{}
	ev := make(chan Event, 16)
	for _, e := range []Event{
		{313, 1}, {311, 1}, {313, 2}, {313, 0}, // Enter while talking: ignored; repeat: ignored
		{311, 1}, {311, 0}, {310, 1}, {999, 1},
	} {
		ev <- e
	}
	close(ev)
	var said []string
	PTT(context.Background(), ev, b, rec, rem, func(s string) { said = append(said, s) }, nil)
	if len(rem.sent) != 1 || strings.Join(rem.keys, ",") != "enter,clear" || rec.on {
		t.Fatalf("sent %v keys %v recording %v", rem.sent, rem.keys, rec.on)
	}
	if strings.Join(said, "|") != "listening…|transcribing…|typed: hello|sent|cleared" {
		t.Fatal(said)
	}

	// A tap (too short to send) sends nothing.
	rec.clip = nil
	rem.sent = nil
	ev = make(chan Event, 2)
	ev <- Event{313, 1}
	ev <- Event{313, 0}
	close(ev)
	PTT(context.Background(), ev, b, rec, rem, nil, nil)
	if len(rem.sent) != 0 {
		t.Fatal("a tap was sent")
	}
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o755) }
