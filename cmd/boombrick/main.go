// Command boombrick is the music player for the TrimUI Brick.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/ujjwal313/boombrick/assets"
	"github.com/ujjwal313/boombrick/internal/app"
	"github.com/ujjwal313/boombrick/internal/art"
	"github.com/ujjwal313/boombrick/internal/audio/decode"
	"github.com/ujjwal313/boombrick/internal/background"
	"github.com/ujjwal313/boombrick/internal/download"
	"github.com/ujjwal313/boombrick/internal/features"
	"github.com/ujjwal313/boombrick/internal/fonts"
	"github.com/ujjwal313/boombrick/internal/importer"
	"github.com/ujjwal313/boombrick/internal/library"
	"github.com/ujjwal313/boombrick/internal/logx"
	"github.com/ujjwal313/boombrick/internal/lyrics"
	"github.com/ujjwal313/boombrick/internal/netx"
	"github.com/ujjwal313/boombrick/internal/perf"
	"github.com/ujjwal313/boombrick/internal/platform"
	"github.com/ujjwal313/boombrick/internal/player"
	"github.com/ujjwal313/boombrick/internal/release"
	"github.com/ujjwal313/boombrick/internal/resume"
	"github.com/ujjwal313/boombrick/internal/selfupdate"
	"github.com/ujjwal313/boombrick/internal/settings"
	"github.com/ujjwal313/boombrick/internal/space"
	"github.com/ujjwal313/boombrick/internal/spotify"
	"github.com/ujjwal313/boombrick/internal/store"
	"github.com/ujjwal313/boombrick/internal/ui/text"
	"github.com/ujjwal313/boombrick/internal/version"
	"github.com/ujjwal313/boombrick/internal/ytmusic"
)

func init() {
	// SDL must be driven from the main OS thread.
	runtime.LockOSThread()
}

// releaseEvery is how often Settings asks GitHub for the latest releases.
const releaseEvery = 6 * time.Hour

func main() {
	started := time.Now()
	showVersion := flag.Bool("version", false, "print the version and exit")
	inBackground := flag.Bool("background", false, "play the saved queue with no screen until stopped, as Play in background starts it")
	music := flag.String("music", "", "desktop only: the music folder to play, instead of $BOOMBRICK_DATA/Music")
	flag.Parse()
	if *music != "" {
		os.Setenv("BOOMBRICK_MUSIC", *music) // the background player inherits it
	}
	defer finishCaptures()
	if *showVersion {
		fmt.Println(version.String())
		return
	}
	mode := func() error { return run(started) }
	if *inBackground {
		mode = runBackground
	}
	if err := mode(); err != nil {
		fmt.Fprintln(os.Stderr, "boombrick:", err)
		os.Exit(1)
	}
}

func run(started time.Time) error {
	// Keep the heap well inside the Brick's 1 GB, next to NextUI.
	debug.SetMemoryLimit(96 << 20)

	// The program's folder holds QuickJS and, on the Brick, the app that
	// updates are staged in; on CrossMix and Knulli, Boombrick's data too.
	dir := programDir()
	prof := platform.Detect(dir)
	firmware = prof.Firmware
	paths := prof.Paths
	for _, d := range []string{paths.Data, paths.Logs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	logFile, err := logx.Open(filepath.Join(paths.Logs, "Boombrick.txt"), 1<<20, 1)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// On the device stderr is the crash file; keep the log out of it.
	var logOut io.Writer = logFile
	if !paths.OnDevice {
		logOut = io.MultiWriter(os.Stderr, logFile)
	}
	log := slog.New(slog.NewTextHandler(logOut, nil))
	log.Info("starting", "version", version.Version, "firmware", prof.Firmware, "data", paths.Data)
	// Perf mode logs the launch's stages, then memory, CPU and frame times
	// every 10 s.
	var rec *perf.Recorder
	if perf.Enabled(dir) {
		rec = perf.Start(log, started, 10*time.Second)
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
	defer prefs.Close() // writes what is waiting before the database closes
	rec.Mark("database")

	// The files the library reads, whether a scan finds them or a download
	// files them.
	formats := library.Formats{Supported: decode.Supported, Duration: decode.Duration}

	// Album art and lyrics are found off the UI thread; what isn't with the
	// music is looked up online while the device is. They close after the
	// player, so the music stops at once on quitting, and before the database.
	web := netx.NewClient()
	// lookups is whether covers, lyrics and fonts may be fetched online. In
	// the offline build (features.NetLookups off) it is never: they come
	// only from the files and folders on the card.
	lookups := online
	if !features.NetLookups {
		lookups = func() bool { return false }
	}
	covers := art.New(art.Options{
		DB:     db.DB,
		Dir:    filepath.Join(paths.Data, "cache", "art"),
		Lookup: art.NewLookup(web),
		Online: lookups,
		Log:    log,
	})
	defer covers.Close()
	// Fonts for other scripts and emoji come from the fonts release the first
	// time text needs one: on screen, in the library after a scan, or in
	// lyrics as they are found. Each release's are kept in a folder of their
	// own, and older releases' removed. The store closes after the app.
	fontsDir := filepath.Join(paths.Data, "fonts")
	if err := fonts.Prune(fontsDir, text.FontsRelease); err != nil {
		log.Warn("remove old fonts", "err", err)
	}
	fontStore := fonts.New(fonts.Options{Dir: filepath.Join(fontsDir, text.FontsRelease), HTTP: web,
		Base:  fonts.Release(text.FontsRelease),
		Fonts: text.Catalog, Online: lookups, Log: log})
	defer fontStore.Close()
	needs, err := text.NewNeeds(assets.Fonts(), text.Catalog)
	if err != nil {
		return err
	}
	fetchFonts := func(ss ...string) {
		for _, s := range ss {
			for _, name := range needs.Of(s, fontStore.Has) {
				fontStore.Want(name)
			}
		}
	}
	words := lyrics.New(lyrics.Options{DB: db.DB, Fetch: lyrics.NewClient(web), Online: lookups, Log: log,
		FileOf: func(id string) (string, bool) {
			path, ok, err := library.FileOf(db.DB, id)
			return path, ok && err == nil
		},
		Found: func(l lyrics.Lyrics) {
			for _, line := range l.Lines {
				fetchFonts(line.Text)
			}
		}})
	defer words.Close()
	// YouTube Music is asked, its thumbnails fetched and its songs downloaded
	// off the UI thread too. The searcher falls back to yt-dlp's search.
	// None of it exists in the offline build (features.Streaming off).
	var (
		thumbs *art.Web
		dl     *download.Service
		yt     *ytmusic.Searcher
		im     *importer.Importer
	)
	if features.Streaming {
		thumbs = art.NewWeb(web, online, log)
		defer thumbs.Close()
		ytc := ytmusic.NewClient(web)
		qjs := quickJS(dir)
		dl, err = download.New(download.Options{DB: db.DB, Music: paths.Music, Data: paths.Data, QJS: qjs,
			HTTP: web, Albums: ytc, Lyrics: lyrics.NewClient(web), Formats: formats, Online: online,
			Quality: func() string { return prefs.String(settings.DownloadQuality) }, Log: log})
		if err != nil {
			return err
		}
		defer dl.Close()
		log.Info("downloads", "qjs", qjs != "")
		yt = ytmusic.NewSearcher(ytmusic.Options{Client: ytc, Online: online, Log: log, Fallback: dl.Search})
		defer yt.Close()
		// Spotify playlists are imported by matching their songs on YouTube
		// Music, a search a second, and downloading the matches. The importer
		// closes before the queue it fills.
		im, err = importer.New(importer.Options{DB: db.DB, Search: ytc.Search, Get: dl.Get, State: dl.State,
			Remove: dl.Remove, Online: online, Log: log})
		if err != nil {
			return err
		}
		defer im.Close()
	}
	// Settings' Storage page measures and clears the caches off the UI
	// thread; the meter closes before the caches it reaches into.
	spaceOpts := space.Options{Art: covers, Lyrics: words, Fonts: fontStore, Log: log}
	if dl != nil {
		spaceOpts.Parts = space.Funcs{SizeOf: dl.Leftovers, Empty: dl.ClearLeftovers}
		spaceOpts.Downloader = filepath.Join(paths.Data, "bin")
	}
	meter := space.New(spaceOpts)
	defer meter.Close()
	// The status bar names the Bluetooth headphones or speaker connected,
	// asking BlueZ on the status reader's goroutine.
	bt := platform.NewBluetooth("/sys")
	defer bt.Close()
	// On the Brick, Power turns the backlight off and holding it shuts down.
	var light app.Backlight
	var powerOff func() error
	if paths.OnDevice {
		light = platform.NewBacklight()
		powerOff = prof.PowerOff
	}
	// Settings says when yt-dlp or Boombrick has a newer release, asking
	// GitHub at most every 6 hours while it is open. On the Brick, Update
	// Boombrick stages a release in the pak's folder, for launch.sh to move
	// in on the next launch.
	// This fork offers neither: upstream's releases would replace it.
	var toolReleases, releases *release.Checker
	var updater app.Updater
	if features.Streaming {
		toolReleases = release.NewChecker(web, "yt-dlp/yt-dlp", online, releaseEvery, log)
		defer toolReleases.Close()
	}
	if features.UpstreamUpdates {
		releases = release.NewChecker(web, "ujjwal313/boombrick", online, releaseEvery, log)
		defer releases.Close()
		if paths.OnDevice && dir != "" {
			up := selfupdate.New(web, dir, selfupdate.Pak{Asset: prof.ReleaseAsset, Dir: prof.ReleaseDir, Launcher: prof.Launcher}, log)
			defer up.Close()
			updater = up
		}
	}

	// Spotify runs in its helper, started the first time a Spotify song is
	// played; it stops after the player. The library the Spotify screens
	// ask, kept in the database, stops before the helper.
	var sp *spotify.Helper
	var spLib *spotify.Library
	if features.Streaming {
		sp = newHelper(dir, paths, log, logOut)
		defer sp.Close()
		spLib = spotify.NewLibrary(sp, db.DB, log)
		defer spLib.Close()
	}

	notices := make(chan player.Notice, 8)
	p := newPlayer(db.DB, prefs, sp, online, log, func(n player.Notice) {
		select {
		case notices <- n:
		default: // a burst of them: the toast shows the latest anyway
		}
	})
	defer p.Close()
	// The equaliser runs in the player's output path; it is written out as
	// Boombrick quits, after the player has stopped.
	eq, err := openEQ(paths)
	if err != nil {
		return err
	}
	defer eq.Flush()
	p.SetProcessor(eq.Processor())
	log.Info("equaliser", "profile", eq.Name(), "bypassed", eq.Bypassed(), "bands", len(eq.Profile().Filters))

	// A background player gives the music back before the saved queue is
	// read; neither must hold up the first frame. Every later release keeps
	// this take-back at launch, as only it can stop a background player an
	// earlier version left playing.
	restored := make(chan struct{})
	go func() {
		defer close(restored)
		background.TakeBack(prof.PlayerLock(), 5*time.Second, p, background.Store{
			Load:         func() (player.Snapshot, bool, error) { return resume.Load(db.DB) },
			LoadHandover: func() (resume.Handover, error) { return resume.LoadHandover(db.DB) },
			SaveHandover: func(h resume.Handover) error { return resume.SaveHandover(db.DB, h) },
		}, log)
		// A background player stopping by itself just as it was taken back can
		// remove the file the launcher has just written.
		if f := prof.StayAwake(); f != "" {
			if err := os.WriteFile(f, []byte("1\n"), 0o644); err != nil {
				log.Warn("keep the Brick awake", "err", err)
			}
		}
	}()
	gate := &saves{saver: resume.NewSaver(db.DB).Save}
	save := func() {
		if s, ok := p.Snapshot(); ok {
			if err := gate.save(s); err != nil {
				log.Warn("save queue", "err", err)
			}
		}
	}
	defer save() // runs before the player and the database close

	// Quitting cancels the scanner and the saver, and waits for them and the
	// restore before the database closes under them.
	ctx, cancel := context.WithCancel(context.Background())
	scanDone, saved := make(chan struct{}), make(chan struct{})
	defer func() { cancel(); <-scanDone; <-saved; <-restored }()

	// Scans run one at a time on their own goroutine: at start-up, then each
	// time the app asks. Asking while a scan runs queues one more. The app
	// hears when a scan writes to the library, at most every 10 s while it
	// runs, and when each scan ends.
	scanReq, scanned := make(chan struct{}, 1), make(chan app.ScanUpdate, 1)
	// tell hands u to the app, merged with one it hasn't taken yet. The
	// scanner is the only sender, so there is room after the drain.
	tell := func(u app.ScanUpdate) {
		select {
		case old := <-scanned:
			u.Changed, u.Ended = u.Changed || old.Changed, u.Ended || old.Ended
		default:
		}
		scanned <- u
	}
	scanReq <- struct{}{}
	go func() {
		defer close(scanDone)
		defer close(scanned)
		for {
			select {
			case <-ctx.Done():
				return
			case <-scanReq:
			}
			if ctx.Err() != nil {
				return
			}
			start := time.Now()
			var told time.Time
			wroteAny := false
			wrote := func(library.Stats) {
				wroteAny = true
				if time.Since(told) >= 10*time.Second {
					told = time.Now()
					tell(app.ScanUpdate{Changed: true})
				}
			}
			st, err := library.ScanWithProgress(ctx, db.DB, paths.Music, formats, wrote)
			changed := false
			switch {
			case errors.Is(err, context.Canceled):
				return
			case err != nil:
				log.Error("scan", "root", paths.Music, "err", err)
				changed = wroteAny // batches already written are worth loading
			default:
				log.Info("scanned", "root", paths.Music, "tracks", st.Total, "added", st.Added,
					"updated", st.Updated, "removed", st.Removed, "took", time.Since(start).Round(time.Millisecond))
				changed = st.Added+st.Updated+st.Removed > 0
				// Downloads whose files were deleted, as on a PC, can be had again.
				if dl != nil {
					dl.Prune()
				}
			}
			tell(app.ScanUpdate{Changed: changed, Ended: true})
			// The fonts the library's names need, fetched before they show.
			if texts, err := library.Texts(db.DB); err == nil {
				fetchFonts(texts...)
			} else {
				log.Warn("read the library's names", "err", err)
			}
		}
	}()
	rescan := func() {
		select {
		case scanReq <- struct{}{}:
		default:
		}
	}
	// Save the queue every 5 s while music plays. The app reloads Continue
	// listening when a save moves to another track or playback stops.
	savedTrack := make(chan struct{}, 1)
	go func() {
		defer close(saved)
		defer close(savedTrack)
		keepSaving(ctx, 5*time.Second, p.State, save, savedTrack)
	}()

	// NextUI's volume keys change the volume NextUI keeps in a file, read for
	// the volume pill; the app has it read when a button is pressed.
	var volume *platform.VolumeWatcher
	if f := prof.VolumeSettings(); f != "" {
		var ok bool
		volume, ok = platform.NewVolumeWatcher(f)
		log.Info("volume", "source", f, "ok", ok)
	}

	opts := app.Options{
		Fullscreen: paths.OnDevice,
		Log:        log,
		Started:    started,
		Player:     p,
		MusicDir:   paths.Music,
		Load:       func() (*library.Index, error) { return library.Load(db.DB) },
		Progress:   func() ([]resume.Progress, error) { return resume.Recent(db.DB, 8) },
		Rescan:     rescan,
		Scanned:    scanned,
		Saved:      savedTrack,
		Status: func() platform.Status {
			st := platform.ReadStatus("/sys")
			st.Bluetooth = bt.Connected()
			return st
		},
		Covers:        covers,
		Lyrics:        words,
		Prefs:         prefs,
		Space:         meter,
		Backlight:     light,
		PowerOff:      powerOff,
		FirmwarePower: prof.FirmwarePower(),
		QuitChord:     prof.QuitChord(),
		Pads:          prof.Pads(),
		Fonts:         fontStore,
		Version:       version.Version,
		Device:        prof.Device(),
		Updater:       updater,
		Perf:          rec,
		Notices:       notices,
		Offline:       !features.Streaming,
		EQ:            eq,
		Script:        desktopScript(paths.OnDevice),
		Background: func() error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			// The background player reads the EQ from its files.
			if err := eq.Flush(); err != nil {
				log.Warn("save the equaliser", "err", err)
			}
			return gate.handOver(func() error {
				return background.HandOver(p, gate.now, func(h resume.Handover) error { return resume.SaveHandover(db.DB, h) },
					exe, os.Environ(), handOverWait(p.State()))
			})
		},
	}
	// A nil pointer would make an interface that isn't nil: only what
	// exists is set.
	if volume != nil {
		opts.Volume = volume
	}
	if features.Streaming {
		opts.Thumbs, opts.YouTube, opts.Downloads = thumbs, yt, dl
		opts.Spotify, opts.Importer = spLib, im
		opts.ToolReleases = toolReleases
	}
	if releases != nil {
		opts.Releases = releases
	}
	a, err := app.New(opts)
	if err != nil {
		return err
	}
	defer a.Close()
	// The volume watcher stops before the app and SDL close under it.
	if volume != nil {
		vol, stop := context.WithCancel(context.Background())
		defer stop()
		go volume.Watch(vol, a.VolumeChanged)
	}
	rec.Mark("window")

	// A signal from the launcher ends the run like a quit, so the queue is saved.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	// The first signal quits; if that hangs, or a second signal comes, the
	// process exits regardless.
	go func() {
		<-sig
		a.Quit()
		select {
		case <-sig:
			log.Warn("second signal, exiting")
		case <-time.After(3 * time.Second):
			log.Error("quit timed out")
		}
		os.Exit(1)
	}()
	return a.Run()
}

// programDir is the program's folder: the pak on the Brick, dist/desktop on a
// desktop. "" when it can't be found.
func programDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// quickJS is QuickJS, yt-dlp's JavaScript runtime, as the pak and the
// desktop build put it: bin/qjs in the program's folder, dir. "" when it
// isn't there.
func quickJS(dir string) string {
	if dir == "" {
		return ""
	}
	p := filepath.Join(dir, "bin", "qjs")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// desktopScript is $BOOMBRICK_SCRIPT on a desktop, which drives the app for
// tests and screenshots; never on a device.
func desktopScript(onDevice bool) string {
	if onDevice {
		return ""
	}
	return os.Getenv("BOOMBRICK_SCRIPT")
}
