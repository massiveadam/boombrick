// Command rgsp-voice is push-to-talk from the handheld to a coding agent in a
// tmux session on the home server.
//
// On the server, with a transcriber that speaks the OpenAI API (speaches, or
// whisper.cpp's server) and a tmux session the agent runs in:
//
//	rgsp-voice serve -listen 100.x.y.z:8178 -token-file ~/.config/rgsp-voice/token \
//	    -stt http://127.0.0.1:8000/v1/audio/transcriptions -model Systran/faster-whisper-small.en \
//	    -tmux pocket
//
// On the handheld, alongside the terminal (its HTTP_PROXY set for Tailscale's
// userspace mode):
//
//	rgsp-voice ptt -server http://sanchez:8178 -token-file token.txt \
//	    -input /dev/input/event1 -talk 313 -enter 312 -clear 0
//
// Hold the talk button, speak, let go: the text is typed into the agent's
// prompt, not entered. Read it, then press the enter button. To find a
// button's code: rgsp-voice keys -input /dev/input/event1.
//
// To try the server from any computer with a WAV file:
//
//	rgsp-voice send -server http://sanchez:8178 -token-file token clip.wav
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ujjwal313/boombrick/internal/voice"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "ptt":
		err = ptt(os.Args[2:])
	case "send":
		err = send(os.Args[2:])
	case "keys":
		err = keys(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rgsp-voice:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rgsp-voice serve|ptt|send|keys [flags]  (-h for each)")
	os.Exit(2)
}

func readToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("-token-file is needed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8178", "address to listen on; use the Tailscale address, not 0.0.0.0")
	tokenFile := fs.String("token-file", "", "file holding the shared token (16+ characters)")
	stt := fs.String("stt", "http://127.0.0.1:8000/v1/audio/transcriptions", "OpenAI-compatible transcriptions URL")
	model := fs.String("model", "Systran/faster-whisper-small.en", "transcription model")
	lang := fs.String("language", "en", "spoken language; empty to detect")
	target := fs.String("tmux", "pocket", "tmux target the text is typed into")
	fs.Parse(args)
	tok, err := readToken(*tokenFile)
	if err != nil {
		return err
	}
	if host, _, err := net.SplitHostPort(*listen); err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		return fmt.Errorf("refusing to listen on every interface (%s): give the Tailscale or loopback address", *listen)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := &voice.Server{
		STT:   voice.OpenAI{URL: *stt, Model: *model, Language: *lang},
		Typer: voice.Tmux{Target: *target},
		Token: tok,
		Log:   log,
	}
	if err := s.Check(); err != nil {
		return err
	}
	srv := &http.Server{Addr: *listen, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Info("rgsp-voice serving", "listen", *listen, "tmux", *target, "stt", *stt)
	return srv.ListenAndServe()
}

func client(fs *flag.FlagSet, args []string) (voice.Client, error) {
	server := fs.String("server", "", "http://host:port of rgsp-voice serve")
	tokenFile := fs.String("token-file", "", "file holding the shared token")
	fs.Parse(args)
	if *server == "" {
		return voice.Client{}, errors.New("-server is needed")
	}
	tok, err := readToken(*tokenFile)
	return voice.Client{Base: *server, Token: tok}, err
}

func send(args []string) error {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	key := fs.String("key", "", "send this key instead of a clip (enter, clear, escape, ...)")
	c, err := client(fs, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if *key != "" {
		return c.Key(ctx, *key)
	}
	if fs.NArg() != 1 {
		return errors.New("give one WAV file")
	}
	wav, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	start := time.Now()
	text, err := c.Dictate(ctx, wav)
	if err != nil {
		return err
	}
	fmt.Printf("%q (%v)\n", text, time.Since(start).Round(time.Millisecond))
	return nil
}

func ptt(args []string) error {
	fs := flag.NewFlagSet("ptt", flag.ExitOnError)
	input := fs.String("input", "/dev/input/event1", "the buttons' input device")
	talk := fs.Uint("talk", 0, "key code held to talk (see rgsp-voice keys)")
	enter := fs.Uint("enter", 0, "key code that sends Enter; 0 for none")
	clear := fs.Uint("clear", 0, "key code that clears the typed line; 0 for none")
	device := fs.String("device", "default", "ALSA capture device")
	c, err := client(fs, args)
	if err != nil {
		return err
	}
	if *talk == 0 {
		return errors.New("-talk is needed")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ev := make(chan voice.Event, 16)
	go func() {
		if err := voice.ReadKeys(ctx, *input, ev); err != nil && ctx.Err() == nil {
			log.Error("ptt input", "err", err)
		}
		close(ev)
	}()
	rec := &voice.Recorder{Device: *device}
	b := voice.Buttons{Talk: uint16(*talk), Enter: uint16(*enter), Clear: uint16(*clear)}
	log.Info("push-to-talk ready", "input", *input, "talk", *talk, "enter", *enter)
	voice.PTT(ctx, ev, b, rec, c, func(s string) {
		if s != "" {
			log.Info(s)
		}
	}, log)
	return nil
}

func keys(args []string) error {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	input := fs.String("input", "/dev/input/event1", "input device to watch")
	fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ev := make(chan voice.Event, 16)
	go func() { voice.ReadKeys(ctx, *input, ev); close(ev) }()
	fmt.Println("press buttons; Ctrl-C to stop")
	for e := range ev {
		if e.Value != 2 {
			fmt.Printf("code %d %s\n", e.Code, map[int32]string{0: "up", 1: "down"}[e.Value])
		}
	}
	return nil
}
