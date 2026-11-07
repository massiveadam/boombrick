// Command rgsp-sync copies a music library from a home server onto the
// player's card, one way: the server is only ever read.
//
// On the server (for example in a container on Unraid, with the library
// mounted read-only), list the library:
//
//	rgsp-sync manifest -root /music -out /meta/manifest.json -cache /meta/hashes.json
//
// and serve /music and /meta/manifest.json with any read-only HTTP server.
//
// On the player:
//
//	rgsp-sync pull -from http://server:8080/ -to /mnt/SDCARD/Music/Library [-delete] [-dry-run]
//	rgsp-sync auto -home-ssid MyWifi -from ... -to ...   # only at home, and with charge to spare
//
// pull and auto can be interrupted at any point and run again: partial
// files are resumed and every file is checked against its SHA-256 before it
// is put in place. Only files rgsp-sync put there itself are ever deleted.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ujjwal313/boombrick/internal/musicsync"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "manifest":
		err = manifest(os.Args[2:])
	case "pull":
		err = pull(os.Args[2:], false)
	case "auto":
		err = pull(os.Args[2:], true)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rgsp-sync:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rgsp-sync manifest|pull|auto [flags]  (-h for each)")
	os.Exit(2)
}

func manifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ExitOnError)
	root := fs.String("root", "", "the library, which is only read")
	out := fs.String("out", "", "where to write the manifest; must be outside the library")
	cache := fs.String("cache", "", "a file outside the library that keeps hashes between runs")
	fs.Parse(args)
	if *root == "" || *out == "" {
		return errors.New("-root and -out are needed")
	}
	if within(*root, *out) {
		return fmt.Errorf("%s is inside the library; the library is never written", *out)
	}
	start := time.Now()
	m, err := musicsync.BuildManifest(*root, musicsync.BuildOptions{Cache: *cache})
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	tmp := *out + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, *out); err != nil {
		return err
	}
	var total int64
	for _, e := range m.Files {
		total += e.Size
	}
	fmt.Printf("%d files, %.1f MB, listed in %v\n", len(m.Files), float64(total)/1e6, time.Since(start).Round(time.Millisecond))
	return nil
}

func pull(args []string, auto bool) error {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	from := fs.String("from", "", "http(s)://… base URL of the library, or a folder (with -manifest)")
	man := fs.String("manifest", "", "the manifest: a URL or file; default <from>/manifest.json for a URL")
	to := fs.String("to", "", "the folder on the card to keep in step")
	del := fs.Bool("delete", false, "remove files this tool synced earlier that the server no longer lists")
	dry := fs.Bool("dry-run", false, "show what would happen, change nothing")
	reserve := fs.Int64("reserve-mb", 512, "space to leave free on the card, MB")
	home := fs.String("home-ssid", "", "auto: comma-separated Wi-Fi networks that are home")
	minBat := fs.Int("min-battery", 40, "auto: least battery, percent, to sync without a charger")
	fs.Parse(args)
	if *from == "" || *to == "" {
		return errors.New("-from and -to are needed")
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if auto {
		g := musicsync.Gate{HomeSSIDs: splitList(*home), MinBattery: *minBat,
			SSID: musicsync.CurrentSSID, Battery: musicsync.BatteryFrom("/sys")}
		if ok, why := g.Allow(); !ok {
			log.Info("not syncing", "why", why)
			return nil
		}
	}
	var src musicsync.Source
	if strings.HasPrefix(*from, "http://") || strings.HasPrefix(*from, "https://") {
		base := *from
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		src = musicsync.HTTPSource{Base: base, ManifestURL: *man}
	} else {
		if *man == "" {
			return errors.New("-manifest is needed with a folder")
		}
		src = musicsync.DirSource{Root: *from, ManifestPath: *man}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	start := time.Now()
	res, err := musicsync.Pull(ctx, src, *to, musicsync.Options{DryRun: *dry, Delete: *del, Reserve: *reserve << 20, Log: log})
	verb := "synced"
	if *dry {
		verb = "would sync"
	}
	fmt.Printf("%s: %d to get (%.1f MB), %d to delete, %d up to date; got %d, deleted %d, failed %d, in %v\n",
		verb, len(res.Get), float64(res.Bytes)/1e6, len(res.Delete), res.Kept, res.Got, res.Deleted, len(res.Failed),
		time.Since(start).Round(time.Millisecond))
	if *dry {
		for _, e := range res.Get {
			fmt.Println("  get   ", e.Path)
		}
		for _, p := range res.Delete {
			fmt.Println("  delete", p)
		}
	}
	if err == nil && !*dry && res.Got+res.Deleted > 0 {
		// Boombrick rescans the library each time it starts; this stamp is
		// for a running player to notice a sync (not yet read by it).
		stamp := filepath.Join(*to, ".rgsp-sync", "last-change")
		_ = os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
	}
	return err
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func within(root, p string) bool {
	r, _ := filepath.Abs(root)
	q, _ := filepath.Abs(p)
	rel, err := filepath.Rel(r, q)
	return err == nil && !strings.HasPrefix(rel, "..")
}
