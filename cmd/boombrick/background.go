package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ujjwal313/boombrick/internal/background"
	"github.com/ujjwal313/boombrick/internal/logx"
	"github.com/ujjwal313/boombrick/internal/perf"
	"github.com/ujjwal313/boombrick/internal/platform"
	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/resume"
	"github.com/ujjwal313/boombrick/internal/scrobble"
	"github.com/ujjwal313/boombrick/internal/settings"
	"github.com/ujjwal313/boombrick/internal/store"
)

// runBackground is Boombrick's background player: the saved queue, played
// with no screen until Boombrick takes the music back or it stops by itself.
// It has no art, lyrics, downloads or scans; songs that wait for a download
// are skipped.
func runBackground() error {
	started := time.Now()
	backgroundMode = true
	debug.SetMemoryLimit(48 << 20)
	ready := background.Readier() // before anything is started that could inherit it
	closeInherited()
	signal.Ignore(syscall.SIGHUP)
	dir := programDir()
	prof := platform.Detect(dir)
	firmware = prof.Firmware
	paths := prof.Paths
	if firmware == platform.RGSP {
		outputs = newOutputChoice(paths.Data)
	}
	logPath := filepath.Join(paths.Logs, "Boombrick.txt")
	logFile, err := logx.Open(logPath, 1<<20, 1)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// Nobody reads this process's output: a panic goes to the log, and the
	// crash file the launcher truncates is never written.
	if err := logToStd(logPath); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(logFile, nil)).With("from", "background")
	lock, err := background.Acquire(prof.PlayerLock())
	if err != nil {
		return err
	}
	defer lock.Release()
	log.Info("playing in the background")
	// Perf mode logs the memory and CPU the player uses every 10 s.
	if perf.Enabled(dir) {
		rec := perf.Start(log, started, 10*time.Second)
		defer rec.Close()
	}

	db, err := store.Open(filepath.Join(paths.Data, "boombrick.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	prefs, err := settings.Load(db.DB, log)
	if err != nil {
		return err
	}
	defer prefs.Close()
	sp := newHelper(dir, paths, log, logFile)
	defer sp.Close()
	p := newPlayer(db.DB, prefs, sp, online, log, func(n player.Notice) {
		log.Info("streaming", "notice", int(n.Kind))
	})
	defer p.Close()
	// The same equaliser as in the foreground, from the files it wrote.
	if eq, err := openEQ(paths); err != nil {
		log.Warn("open the equaliser", "err", err)
	} else {
		p.SetProcessor(eq.Processor())
		log.Info("equaliser", "profile", eq.Name(), "bypassed", eq.Bypassed())
		followOutputs(eq, log)
		defer eq.Flush() // what changed on a switch, for Boombrick to open
	}

	snap, ok, err := resume.Load(db.DB)
	if err != nil {
		return err
	}
	if !ok {
		log.Info("nothing queued to play")
		return nil
	}
	if !p.Restore(snap) {
		log.Info("the saved queue can't be played")
		return nil
	}
	h, err := resume.LoadHandover(db.DB)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	saver := resume.NewSaver(db.DB)
	// plorpOS, told the music plays, keeps it playing as the screen goes off,
	// and sends its volume keys' music controls here.
	// Listening first: an unasked-for SIGUSR1 would end this process.
	held := obeyControls(ctx, prof.CommandFile(), p, log, true)
	if sc := scrobble.New(db.DB, paths.Data, log); sc != nil {
		go sc.Run(ctx, p)
	}
	announced := announcePlaying(ctx, prof.PlayingFile(), p)
	defer announced()
	err = background.Run(ctx, background.Options{
		Player:     p,
		Save:       saver.Save,
		Handover:   func(h resume.Handover) error { return resume.SaveHandover(db.DB, h) },
		Sleep:      h.Sleep,
		StayAwake:  prof.StayAwake(),
		Ready:      ready,
		Log:        log,
		Every:      time.Second,
		SaveEvery:  5 * time.Second,
		StallLimit: 2 * time.Minute,
		SoundUsers: func() []string { return platform.SoundUsers("/proc") },
		Held:       held,
	})
	log.Info("background play ended", "err", err)
	return err
}

// logToStd points the standard output and error at the log, appended to.
func logToStd(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, fd := range []int{1, 2} {
		if err := syscall.Dup3(int(f.Fd()), fd, 0); err != nil {
			return err
		}
	}
	return nil
}

// closeInherited closes the descriptors the program that started this one
// left open: Go's exec passes on any not marked close-on-exec, and the
// screen's C libraries (SDL, the GPU, the input devices, the sound card) may
// have opened theirs that way. Held for a whole game, they would keep the
// display, the input and the sound from the program that runs it. Go marks
// the ones it opens, and Readier the one it keeps, so what lacks the mark is
// exactly what was inherited.
func closeInherited() {
	d, err := os.Open("/proc/self/fd")
	if err != nil {
		return
	}
	names, _ := d.Readdirnames(-1)
	d.Close() // its own descriptor is gone before the others are looked at
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil || fd <= 2 {
			continue
		}
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
		if errno == 0 && flags&syscall.FD_CLOEXEC == 0 {
			syscall.Close(fd)
		}
	}
}

// announcePlaying keeps path saying, while p plays, that this process's music
// plays (platform.Announce). The function it returns stops that and waits
// until the file is cleared.
func announcePlaying(ctx context.Context, path string, p *player.Player) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		platform.Announce(ctx, path, 500*time.Millisecond, func() bool { return p.State().Playing })
	}()
	return func() { cancel(); <-done }
}

// holdLimit is how long the background player waits, paused from the
// volume keys, to be played again before it saves its place and ends.
const holdLimit = 30 * time.Minute

// obeyControls carries out the music controls plorpOS sends (toggle, pause,
// next, prev: its volume keys, and a headset's own buttons and ear detection)
// on p until ctx ends. The returned function reports whether the music
// was paused by one and is waiting, up to holdLimit, to be played again. In
// the background player (release) a pause lets go of the sound card, so a
// game can have it meanwhile; playing opens it again.
func obeyControls(ctx context.Context, path string, p *player.Player, log *slog.Logger, release bool) func() bool {
	var pausedAt atomic.Int64 // unix nanoseconds of the pause; 0 not paused by a control
	cmds := platform.Commands(ctx, path)
	go func() {
		for c := range cmds {
			log.Info("music control", "do", c)
			switch c {
			case "toggle", "pause":
				playing := p.State().Playing
				if c == "pause" && !playing {
					continue // a pause that finds the music paused does nothing
				}
				p.Toggle()
				if playing {
					pausedAt.Store(time.Now().UnixNano())
					if release {
						p.Release()
					}
				} else {
					pausedAt.Store(0)
				}
			case "next":
				p.Next()
			case "prev":
				p.Prev()
			}
		}
	}()
	return func() bool {
		at := pausedAt.Load()
		return at != 0 && time.Since(time.Unix(0, at)) < holdLimit && !p.State().Playing
	}
}
