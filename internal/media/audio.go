package media

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/rvaccone/sermon-pipeline/internal/command"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// Loudness is an EBU R128 measurement.
type Loudness struct {
	Integrated float64 `json:"integrated_lufs"`
	TruePeak   float64 `json:"true_peak_dbtp"`
	Range      float64 `json:"range_lu"`
}

// Measure reads the loudness of a whole file.
func Measure(ctx context.Context, path string) (Loudness, error) {
	return measure(ctx, "", "-i", path)
}

// measure reads loudness after an optional filter chain. input holds the input options
// (-ss, -t, -i …).
func measure(ctx context.Context, filter string, input ...string) (Loudness, error) {
	chain := "loudnorm=print_format=json"
	if filter != "" {
		chain = filter + "," + chain
	}
	args := append([]string{"-hide_banner", "-nostdin", "-nostats"}, input...)
	args = append(args, "-vn", "-af", chain, "-f", "null", "-")
	stderr, err := command.Stderr(ctx, "ffmpeg", args...)
	if err != nil {
		return Loudness{}, err
	}
	return parseLoudness(stderr)
}

var loudnessJSON = regexp.MustCompile(`(?s)\{[^{}]*"input_i"[^{}]*\}`)

// parseLoudness reads loudnorm's JSON report. Silence measures as "-inf", which is an error here:
// there is nothing to master.
func parseLoudness(stderr []byte) (Loudness, error) {
	m := loudnessJSON.Find(stderr)
	if m == nil {
		return Loudness{}, fmt.Errorf("ffmpeg printed no loudness measurement")
	}
	var raw struct {
		I   string `json:"input_i"`
		TP  string `json:"input_tp"`
		LRA string `json:"input_lra"`
	}
	if err := json.Unmarshal(m, &raw); err != nil {
		return Loudness{}, err
	}
	var l Loudness
	for _, f := range []struct {
		text string
		into *float64
	}{{raw.I, &l.Integrated}, {raw.TP, &l.TruePeak}, {raw.LRA, &l.Range}} {
		v, err := strconv.ParseFloat(f.text, 64)
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return Loudness{}, fmt.Errorf("unusable loudness measurement %q (is the audio silent?)", f.text)
		}
		*f.into = v
	}
	return l, nil
}

// MasterSpec describes one mastered audio output.
type MasterSpec struct {
	Source   string
	Span     timeline.Span
	Channels int     // 2 for video, 1 for the podcast
	Rate     int     // sample rate
	LUFS     float64 // integrated loudness target
	TruePeak float64 // ceiling in dBTP, which must hold after lossy encoding too
	FadeIn   float64
	FadeOut  float64
	Dest     string // WAV
	// Check encodes the master the way it will be delivered and measures the result, so the
	// loudness and peak targets are verified on what listeners actually hear.
	Check func(ctx context.Context, wav string) (Loudness, error)
}

// Shaping is the speech chain the probe settled on: remove rumble, then gently even out the gap
// between the quietest and loudest passages so listeners don't ride the volume.
const shaping = "highpass=f=70,acompressor=threshold=-24dB:ratio=3:attack=15:release=250:knee=6"

// Master renders speech audio at a target loudness and true-peak ceiling.
//
// Gain is applied after measuring the shaped audio, followed by a limiter running at 4× the
// sample rate (so it catches the inter-sample peaks that encoders create). The delivered
// encoding is then measured; if loudness is off by more than half a unit or the peak is over the
// ceiling, the gain and ceiling are corrected and the render repeats, up to three times.
func Master(ctx context.Context, s MasterSpec) (Loudness, error) {
	shaped, err := measure(ctx, shaping, "-ss", timeline.Arg(s.Span.Start), "-t", timeline.Arg(s.Span.Duration()), "-i", s.Source)
	if err != nil {
		return Loudness{}, fmt.Errorf("measuring shaped audio: %w", err)
	}
	gain := s.LUFS - shaped.Integrated
	ceiling := s.TruePeak - 0.5
	var result Loudness
	for attempt := 0; attempt < 3; attempt++ {
		if err := renderMaster(ctx, s, gain, ceiling); err != nil {
			return Loudness{}, err
		}
		if result, err = s.Check(ctx, s.Dest); err != nil {
			return Loudness{}, err
		}
		loudOK := math.Abs(result.Integrated-s.LUFS) <= 0.5
		peakOK := result.TruePeak <= s.TruePeak
		if loudOK && peakOK {
			return result, nil
		}
		gain += s.LUFS - result.Integrated
		if !peakOK {
			ceiling -= result.TruePeak - s.TruePeak + 0.2
		}
	}
	return result, fmt.Errorf("audio missed its target after 3 attempts: %.1f LUFS, %.1f dBTP", result.Integrated, result.TruePeak)
}

func renderMaster(ctx context.Context, s MasterSpec, gainDB, ceilingDB float64) error {
	limit := math.Pow(10, ceilingDB/20)
	chain := fmt.Sprintf("%s,volume=%.2fdB,aresample=%d,alimiter=limit=%.4f:attack=5:release=60:level=false,aresample=%d",
		shaping, gainDB, s.Rate*4, limit, s.Rate)
	if s.FadeIn > 0 {
		chain += fmt.Sprintf(",afade=t=in:d=%.2f", s.FadeIn)
	}
	if s.FadeOut > 0 {
		chain += fmt.Sprintf(",afade=t=out:st=%.3f:d=%.2f", s.Span.Duration()-s.FadeOut, s.FadeOut)
	}
	return FFmpeg(ctx, "-ss", timeline.Arg(s.Span.Start), "-t", timeline.Arg(s.Span.Duration()), "-i", s.Source,
		"-vn", "-af", chain, "-ac", strconv.Itoa(s.Channels), "-ar", strconv.Itoa(s.Rate),
		"-c:a", "pcm_s24le", s.Dest)
}

// Silences finds pauses in span of an audio file: stretches quieter than noiseDB lasting at
// least minSeconds. Returned times are on the file's timeline.
func Silences(ctx context.Context, path string, span timeline.Span, noiseDB, minSeconds float64) ([]timeline.Span, error) {
	stderr, err := command.Stderr(ctx, "ffmpeg", "-hide_banner", "-nostdin", "-nostats",
		"-ss", timeline.Arg(span.Start), "-t", timeline.Arg(span.Duration()), "-i", path,
		"-af", fmt.Sprintf("silencedetect=n=%gdB:d=%g", noiseDB, minSeconds), "-f", "null", "-")
	if err != nil {
		return nil, err
	}
	return parseSilences(string(stderr), span.Start, span.End), nil
}

var silencePattern = regexp.MustCompile(`silence_(start|end): (-?[0-9.]+)`)

func parseSilences(log string, offset, end float64) []timeline.Span {
	var out []timeline.Span
	open := -1.0
	for _, m := range silencePattern.FindAllStringSubmatch(log, -1) {
		t, _ := strconv.ParseFloat(m[2], 64)
		t += offset
		if m[1] == "start" {
			open = math.Max(t, offset)
		} else if open >= 0 {
			out = append(out, timeline.Span{Start: open, End: t})
			open = -1
		}
	}
	if open >= 0 {
		out = append(out, timeline.Span{Start: open, End: end})
	}
	return out
}
