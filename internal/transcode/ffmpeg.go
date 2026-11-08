package transcode

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// FFmpeg encodes with ffmpeg's libopus, at 48 kHz through the SoX resampler.
//
// It is used rather than opusenc because opusenc moves ReplayGain into the
// Opus header's output gain and R128 tags, which the player reads
// differently from the FLAC it came from. ffmpeg keeps the REPLAYGAIN tags as
// they are and leaves the output gain at 0, so a track plays at the same
// level from either file. Embedded pictures are left out (the album's cover
// file is copied beside it instead). The sample count is exact, so albums
// stay gapless.
type FFmpeg struct {
	FFmpeg, FFprobe string // "" for "ffmpeg" and "ffprobe" on PATH
	Bitrate         int    // kbit/s
}

// Settings names the encoding.
func (f FFmpeg) Settings() string { return fmt.Sprintf("libopus vbr %dk 48k soxr", f.Bitrate) }

func (f FFmpeg) bin(name, def string) string {
	if name != "" {
		return name
	}
	return def
}

func run(cmd *exec.Cmd) (string, error) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return "", fmt.Errorf("%s: %v: %s", cmd.Args[0], err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Codec names src's first audio stream's codec.
func (f FFmpeg) Codec(ctx context.Context, src string) (string, error) {
	out, err := run(exec.CommandContext(ctx, f.bin(f.FFprobe, "ffprobe"), "-v", "error",
		"-select_streams", "a:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0", "--", src))
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("no audio stream")
	}
	return strings.TrimSuffix(strings.Fields(out)[0], ","), nil
}

// Encode writes src as Ogg Opus to dst.
func (f FFmpeg) Encode(ctx context.Context, src, dst string) error {
	if f.Bitrate <= 0 {
		return fmt.Errorf("transcode: no bitrate")
	}
	_, err := run(exec.CommandContext(ctx, f.bin(f.FFmpeg, "ffmpeg"), "-nostdin", "-hide_banner", "-v", "error", "-y",
		"-i", src,
		"-map", "0:a:0", "-map_metadata", "0",
		// Down to stereo (or mono) and 48 kHz, Opus's own rate.
		"-af", "aresample=resampler=soxr:precision=28:osr=48000,aformat=sample_rates=48000:channel_layouts=mono|stereo",
		"-c:a", "libopus", "-b:a", fmt.Sprintf("%dk", f.Bitrate), "-vbr", "on",
		"-compression_level", "10", "-application", "audio",
		"-f", "opus", dst))
	return err
}

// Picture writes src's embedded picture to dst plus its extension.
func (f FFmpeg) Picture(ctx context.Context, src, dst string) (string, error) {
	out, err := run(exec.CommandContext(ctx, f.bin(f.FFprobe, "ffprobe"), "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0", "--", src))
	if err != nil {
		return "", err
	}
	var ext, format string
	switch strings.TrimSuffix(strings.TrimSpace(out), ",") {
	case "":
		return "", nil
	case "mjpeg":
		ext, format = ".jpg", "mjpeg"
	case "png":
		ext, format = ".png", "image2"
	default:
		return "", nil // a picture the player can't show
	}
	_, err = run(exec.CommandContext(ctx, f.bin(f.FFmpeg, "ffmpeg"), "-nostdin", "-hide_banner", "-v", "error", "-y",
		"-i", src, "-map", "0:v:0", "-c", "copy", "-frames:v", "1", "-f", format, dst+ext))
	if err != nil {
		return "", err
	}
	return ext, nil
}
