// Package transcript holds the word-timed transcript every later stage reads, and the
// operations on it: finding quoted phrases, rendering lines for prompts, captions, corrections.
package transcript

import (
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// Word is one spoken word with its time on the source timeline.
type Word struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type Transcript struct {
	Words []Word `json:"words"`
	// Repeats are stretches where the recognizer still repeated itself after a retry (a known
	// Whisper failure); the review page flags them.
	Repeats []timeline.Span `json:"repeats,omitempty"`
}

// Within returns the words that start inside span.
func (t Transcript) Within(span timeline.Span) []Word {
	var out []Word
	for _, w := range t.Words {
		if span.Contains(w.Start) {
			out = append(out, w)
		}
	}
	return out
}

// Match is a run of words, by index, that a quoted phrase was found at.
type Match struct {
	First, Last int
}

// Find locates phrase in the transcript, preferring the occurrence that starts closest to near
// and ignoring any that start more than window seconds away. Comparison ignores case,
// punctuation and spacing, so "Matthew 24, 14" matches "Matthew 24:14".
func (t Transcript) Find(phrase string, near, window float64) (Match, bool) {
	target := normalize(phrase)
	if target == "" {
		return Match{}, false
	}
	best, found := Match{}, false
	bestDistance := math.Inf(1)
	for i, w := range t.Words {
		distance := math.Abs(w.Start - near)
		if distance > window || distance >= bestDistance || !strings.HasPrefix(target, normalize(w.Text)) {
			continue
		}
		joined := ""
		for j := i; j < len(t.Words) && len(joined) < len(target); j++ {
			joined += normalize(t.Words[j].Text)
			if joined == target {
				best, found, bestDistance = Match{i, j}, true, distance
				break
			}
			if !strings.HasPrefix(target, joined) {
				break
			}
		}
	}
	return best, found
}

// Lines renders words as timestamped lines for a prompt, e.g. "[24:41] Speaker 1: Amen. It's…".
// label may be nil; otherwise it names the speaker at a time, and returns unclear when speaker
// detection has no answer. A line breaks when a different known speaker starts, never just because
// detection was unsure, so a speaker's first words stay with the rest of their sentence.
func Lines(words []Word, label func(t float64) string, unclear string) string {
	var b strings.Builder
	var line []Word
	lineLabel := ""
	flush := func() {
		if len(line) == 0 {
			return
		}
		fmt.Fprintf(&b, "[%s] ", timeline.Clock(line[0].Start))
		if label != nil {
			if lineLabel == "" {
				lineLabel = unclear
			}
			b.WriteString(lineLabel + ": ")
		}
		b.WriteString(Join(line))
		b.WriteByte('\n')
		line, lineLabel = nil, ""
	}
	known := func(t float64) string {
		if label == nil {
			return ""
		}
		if who := label(t); who != unclear {
			return who
		}
		return ""
	}
	for i, w := range words {
		who := known(w.Start)
		// An unclear word that starts a sentence belongs to whoever speaks next.
		if who == "" && (i == 0 || endsSentence(words[i-1].Text)) {
			for j := i + 1; j < len(words) && words[j].Start-w.Start < 5 && who == ""; j++ {
				who = known(words[j].Start)
			}
		}
		if len(line) > 0 && (w.Start-words[i-1].End > 1.5 || who != "" && lineLabel != "" && who != lineLabel) {
			flush()
		}
		if lineLabel == "" {
			lineLabel = who
		}
		line = append(line, w)
		if len(line) >= 30 || len(line) >= 8 && endsSentence(w.Text) {
			flush()
		}
	}
	flush()
	return b.String()
}

// Join turns words back into readable text.
func Join(words []Word) string {
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

func endsSentence(word string) bool {
	return strings.HasSuffix(word, ".") || strings.HasSuffix(word, "?") || strings.HasSuffix(word, "!")
}

// normalize keeps only lowercase letters and digits, so comparisons survive punctuation,
// capitalization and differences in how words were split.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
