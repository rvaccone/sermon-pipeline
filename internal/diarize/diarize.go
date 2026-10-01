// Package diarize finds who is speaking when, using sherpa-onnx. The sermon stage uses it to
// confirm the boundaries belong to the preacher, and the clip stage to avoid moments where
// someone else is speaking.
package diarize

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/command"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// Turn is a stretch of speech by one speaker.
type Turn struct {
	timeline.Span
	Speaker string `json:"speaker"`
}

// Options configures one diarization run.
type Options struct {
	Audio        string // 16 kHz mono WAV
	Segmentation string // pyannote segmentation model
	Embedding    string // speaker embedding model
}

const (
	minTurn  = 2.0 // speaker blips shorter than this are noise (found in the probe)
	mergeGap = 1.5 // a speaker's turns closer than this are one turn
	// maxFill is the longest gap between two turns by the same speaker that is credited to them
	// when labeling words. Detection drops a phrase here and there in a long talk; in the probe's
	// sermon 48 of 50 such gaps lay between two of the preacher's turns, the longest 40 s.
	maxFill = 60.0
)

// Run diarizes the audio and returns cleaned, merged turns in time order.
func Run(ctx context.Context, o Options) ([]Turn, error) {
	threads := strconv.Itoa(runtime.NumCPU())
	out, err := command.Output(ctx, "sherpa-onnx-offline-speaker-diarization",
		"--clustering.cluster-threshold=0.9",
		"--segmentation.pyannote-model="+o.Segmentation,
		"--segmentation.num-threads="+threads,
		"--embedding.model="+o.Embedding,
		"--embedding.num-threads="+threads,
		o.Audio)
	if err != nil {
		return nil, err
	}
	return Clean(parse(out)), nil
}

// parse reads lines like "12.345 -- 67.890 speaker_00".
func parse(out []byte) []Turn {
	var turns []Turn
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) != 4 || f[1] != "--" || !strings.HasPrefix(f[3], "speaker_") {
			continue
		}
		start, err1 := strconv.ParseFloat(f[0], 64)
		end, err2 := strconv.ParseFloat(f[2], 64)
		if err1 == nil && err2 == nil && end > start {
			turns = append(turns, Turn{timeline.Span{Start: start, End: end}, f[3]})
		}
	}
	return turns
}

// Clean merges a speaker's turns separated by short gaps, then drops the blips that remain.
// Merging first keeps a voice that was detected in short pieces; the gap limit keeps a speaker
// from "talking" through minutes of music.
func Clean(turns []Turn) []Turn {
	sort.Slice(turns, func(i, j int) bool { return turns[i].Start < turns[j].Start })
	var merged []Turn
	for _, t := range turns {
		if n := len(merged); n > 0 && merged[n-1].Speaker == t.Speaker && t.Start-merged[n-1].End < mergeGap {
			merged[n-1].End = max(merged[n-1].End, t.End)
			continue
		}
		merged = append(merged, t)
	}
	out := merged[:0]
	for _, t := range merged {
		if t.Duration() >= minTurn {
			out = append(out, t)
		}
	}
	return out
}

// At returns the speaker talking at t, or "" if nobody is.
func At(turns []Turn, t float64) string {
	for _, turn := range turns {
		if turn.Contains(t) {
			return turn.Speaker
		}
	}
	return ""
}

// Speaking returns who is talking at t: the speaker of the turn containing t or, in a gap of at
// most maxFill seconds between two turns by the same speaker, that speaker. turns must be in time
// order. It is for labeling words, which are known to be speech; At is for asking whether anyone
// is speaking at all.
func Speaking(turns []Turn, t float64) string {
	if who := At(turns, t); who != "" {
		return who
	}
	i := sort.Search(len(turns), func(i int) bool { return turns[i].Start > t })
	if i == 0 || i == len(turns) {
		return ""
	}
	before, after := turns[i-1], turns[i]
	if before.Speaker == after.Speaker && after.Start-before.End <= maxFill {
		return before.Speaker
	}
	return ""
}

// Main returns the speaker with the most talk time inside span: in a sermon, the preacher.
func Main(turns []Turn, span timeline.Span) string {
	talk := map[string]float64{}
	for _, t := range turns {
		talk[t.Speaker] += t.Overlap(span)
	}
	best, most := "", 0.0
	for speaker, secs := range talk {
		if secs > most || secs == most && speaker < best {
			best, most = speaker, secs
		}
	}
	return best
}

// Others returns the turns inside span by anyone other than speaker.
func Others(turns []Turn, span timeline.Span, speaker string) []Turn {
	var out []Turn
	for _, t := range turns {
		if t.Speaker != speaker && t.Overlap(span) > 0 {
			out = append(out, t)
		}
	}
	return out
}

// Unclear labels a moment when speaker detection has no answer (often a short phrase).
const Unclear = "(unclear)"

// Neutral names speakers "Speaker 1", "Speaker 2", … in order of total talk time, without
// presuming who the preacher is.
func Neutral(turns []Turn) func(t float64) string {
	talk := map[string]float64{}
	for _, t := range turns {
		talk[t.Speaker] += t.Duration()
	}
	speakers := make([]string, 0, len(talk))
	for s := range talk {
		speakers = append(speakers, s)
	}
	sort.Slice(speakers, func(i, j int) bool {
		if talk[speakers[i]] != talk[speakers[j]] {
			return talk[speakers[i]] > talk[speakers[j]]
		}
		return speakers[i] < speakers[j]
	})
	names := map[string]string{}
	for i, s := range speakers {
		names[s] = fmt.Sprintf("Speaker %d", i+1)
	}
	return labeler(turns, names)
}

// WithPreacher names the preacher "Preacher" and everyone else "Speaker 2", "Speaker 3", …
func WithPreacher(turns []Turn, preacher string) func(t float64) string {
	names := map[string]string{}
	if preacher != "" {
		names[preacher] = "Preacher"
	}
	for _, t := range turns {
		if _, ok := names[t.Speaker]; !ok {
			names[t.Speaker] = fmt.Sprintf("Speaker %d", len(names)+1)
		}
	}
	return labeler(turns, names)
}

func labeler(turns []Turn, names map[string]string) func(t float64) string {
	return func(t float64) string {
		if who := Speaking(turns, t); who != "" {
			return names[who]
		}
		return Unclear
	}
}
