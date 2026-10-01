// Package media wraps ffmpeg: probing the source, analysis audio, loudness, pauses, mastering,
// and rendering the sermon video and podcast.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/command"
)

// Info describes the source recording.
type Info struct {
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
}

// Probe reads the recording's duration, frame size and frame rate.
func Probe(ctx context.Context, path string) (Info, error) {
	out, err := command.Output(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,avg_frame_rate,r_frame_rate:format=duration",
		"-of", "json", path)
	if err != nil {
		return Info{}, err
	}
	var doc struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			AvgRate  string `json:"avg_frame_rate"`
			BaseRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return Info{}, fmt.Errorf("reading ffprobe output: %w", err)
	}
	if len(doc.Streams) == 0 {
		return Info{}, fmt.Errorf("%s has no video stream", path)
	}
	s := doc.Streams[0]
	duration, err := strconv.ParseFloat(doc.Format.Duration, 64)
	if err != nil || duration <= 0 {
		return Info{}, fmt.Errorf("%s has no readable duration", path)
	}
	fps := parseRate(s.AvgRate)
	if fps <= 0 {
		fps = parseRate(s.BaseRate)
	}
	if fps <= 0 || s.Width == 0 || s.Height == 0 {
		return Info{}, fmt.Errorf("%s has no readable frame rate or size", path)
	}
	return Info{Duration: duration, Width: s.Width, Height: s.Height, FPS: fps}, nil
}

// parseRate reads ffprobe's "30000/1001" style frame rates.
func parseRate(r string) float64 {
	num, den, ok := strings.Cut(r, "/")
	n, _ := strconv.ParseFloat(num, 64)
	if !ok {
		return n
	}
	d, _ := strconv.ParseFloat(den, 64)
	if d == 0 {
		return 0
	}
	return n / d
}

// ExtractAnalysisAudio writes the 16 kHz mono WAV that transcription and diarization read.
func ExtractAnalysisAudio(ctx context.Context, src, dst string) error {
	return FFmpeg(ctx, "-i", src, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", dst)
}

// FFmpeg runs ffmpeg quietly, overwriting outputs.
func FFmpeg(ctx context.Context, args ...string) error {
	return command.Run(ctx, "ffmpeg", append([]string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y"}, args...)...)
}
