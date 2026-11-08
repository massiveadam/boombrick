package transcode

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFFmpegEncode encodes a real FLAC with the real ffmpeg, when there is
// one: the Opus file must keep the ReplayGain tags, have no output gain, and
// decode to exactly the source's length at 48 kHz, which gapless playback
// needs.
func TestFFmpegEncode(t *testing.T) {
	for _, b := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip(b, "not found")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.flac")
	pic := filepath.Join(dir, "pic.png")
	// 1.5 s at 44.1 kHz with a cover: 66150 samples, 72000 at 48 kHz.
	mk := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=16x16:d=1", "-frames:v", "1", pic)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	mk = exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=f=440:r=44100:d=1.5", "-i", pic,
		"-map", "0", "-map", "1", "-c:a", "flac", "-c:v", "copy", "-disposition:v", "attached_pic",
		"-metadata", "title=Test", "-metadata", "REPLAYGAIN_TRACK_GAIN=-6.50 dB",
		"-metadata", "REPLAYGAIN_ALBUM_GAIN=-7.25 dB", src)
	if out, err := mk.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	f := FFmpeg{Bitrate: 256}
	ctx := context.Background()
	if c, err := f.Codec(ctx, src); err != nil || c != "flac" {
		t.Fatalf("codec %q, %v", c, err)
	}
	dst := filepath.Join(dir, ".out.opus.part") // as Run names it: no telling extension
	if err := f.Encode(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(b, []byte("OpusHead"))
	if i < 0 || len(b) < i+19 {
		t.Fatal("no OpusHead")
	}
	if g := int16(binary.LittleEndian.Uint16(b[i+16:])); g != 0 {
		t.Fatalf("output gain %d, want 0", g)
	}
	for _, tag := range []string{"REPLAYGAIN_TRACK_GAIN=-6.50 dB", "REPLAYGAIN_ALBUM_GAIN=-7.25 dB", "title=Test"} {
		if !bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(tag))) {
			t.Errorf("tag %q lost", tag)
		}
	}
	if bytes.Contains(b, []byte("METADATA_BLOCK_PICTURE")) {
		t.Error("embedded picture kept")
	}
	dec := exec.Command("ffmpeg", "-v", "error", "-i", dst, "-f", "s16le", "-ac", "1", "-")
	pcm, err := dec.Output()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(pcm) / 2; n != 72000 {
		t.Errorf("decodes to %d samples, want 72000", n)
	}

	ext, err := f.Picture(ctx, src, filepath.Join(dir, ".cover.part"))
	if err != nil || ext != ".png" {
		t.Fatalf("picture %q, %v", ext, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".cover.part.png")); !strings.HasPrefix(string(b), "\x89PNG") {
		t.Fatal("cover isn't the PNG")
	}
	if ext, err := f.Picture(ctx, dst, filepath.Join(dir, ".none")); err != nil || ext != "" {
		t.Fatalf("a file without a picture gave %q, %v", ext, err)
	}
}
