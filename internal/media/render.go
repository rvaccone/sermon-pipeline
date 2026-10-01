package media

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// VideoSpec describes the full sermon video.
type VideoSpec struct {
	Source string
	Span   timeline.Span
	FPS    float64
	Audio  string // mastered WAV covering exactly Span
	Preset string // x264 preset
	CRF    int
	Dest   string
}

// Fades at the start and end of the sermon video. The mastered audio must use the same values.
const (
	FadeIn  = 0.5
	FadeOut = 1.0
)

// RenderSermon encodes the sermon once, straight from the source, with short fades. x264 at a
// low CRF won the probe's VMAF comparison against hardware encoding; keyframes every two seconds
// follow YouTube's upload recommendation.
func RenderSermon(ctx context.Context, s VideoSpec) error {
	d := s.Span.Duration()
	fades := fmt.Sprintf("fade=t=in:st=0:d=%g,fade=t=out:st=%.3f:d=%g", FadeIn, d-FadeOut, FadeOut)
	return FFmpeg(ctx,
		"-ss", timeline.Arg(s.Span.Start), "-t", timeline.Arg(d), "-i", s.Source,
		"-i", s.Audio,
		"-map", "0:v:0", "-map", "1:a:0",
		"-vf", fades,
		"-c:v", "libx264", "-preset", s.Preset, "-crf", strconv.Itoa(s.CRF),
		"-profile:v", "high", "-pix_fmt", "yuv420p",
		"-g", strconv.Itoa(int(math.Round(s.FPS*2))), "-bf", "2",
		"-c:a", "aac", "-b:a", "320k",
		"-movflags", "+faststart", "-shortest", s.Dest)
}

// PodcastSpec describes the podcast episode file.
type PodcastSpec struct {
	Audio   string // mastered mono WAV
	Bitrate string
	Title   string
	Artist  string
	Album   string
	Date    string // YYYY-MM-DD
	Comment string
	Dest    string
}

// EncodePodcast writes the MP3 with ID3 tags.
func EncodePodcast(ctx context.Context, p PodcastSpec) error {
	return FFmpeg(ctx, "-i", p.Audio,
		"-c:a", "libmp3lame", "-b:a", p.Bitrate, "-id3v2_version", "3",
		"-metadata", "title="+p.Title,
		"-metadata", "artist="+p.Artist,
		"-metadata", "album="+p.Album,
		"-metadata", "date="+p.Date,
		"-metadata", "comment="+p.Comment,
		p.Dest)
}

// CheckMP3 encodes a WAV the way the podcast is delivered and measures it.
func CheckMP3(bitrate string) func(context.Context, string) (Loudness, error) {
	return func(ctx context.Context, wav string) (Loudness, error) {
		return measureEncoded(ctx, wav, "libmp3lame", bitrate, "mp3")
	}
}

// CheckAAC encodes a WAV the way the video's audio is delivered and measures it.
func CheckAAC(bitrate string) func(context.Context, string) (Loudness, error) {
	return func(ctx context.Context, wav string) (Loudness, error) {
		return measureEncoded(ctx, wav, "aac", bitrate, "adts")
	}
}

// measureEncoded encodes to a temporary file, measures it, and removes it.
func measureEncoded(ctx context.Context, wav, codec, bitrate, format string) (Loudness, error) {
	encoded := wav + ".check." + format
	defer os.Remove(encoded)
	if err := FFmpeg(ctx, "-i", wav, "-c:a", codec, "-b:a", bitrate, "-f", format, encoded); err != nil {
		return Loudness{}, err
	}
	return Measure(ctx, encoded)
}
