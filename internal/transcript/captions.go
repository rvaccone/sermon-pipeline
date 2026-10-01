package transcript

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// Cue is one caption: a short run of words shown together.
type Cue struct {
	Words []Word
	Start float64
	End   float64
}

func (c Cue) Text() string { return Join(c.Words) }

// CueLimits shapes how words are grouped into cues.
type CueLimits struct {
	MaxChars   int     // characters per cue
	MaxWords   int     // words per cue (0 = no limit)
	MaxSeconds float64 // duration per cue
	BreakPause float64 // a pause this long always starts a new cue
}

// YouTubeLimits suit closed captions for the full sermon video.
var YouTubeLimits = CueLimits{MaxChars: 42, MaxSeconds: 5, BreakPause: 0.6}

// Cues groups words into captions, breaking at sentence ends, pauses and the limits.
func Cues(words []Word, lim CueLimits) []Cue {
	var cues []Cue
	var cur []Word
	chars := 0 // length of the current cue's text, spaces included
	flush := func() {
		if len(cur) > 0 {
			cues = append(cues, Cue{Words: cur, Start: cur[0].Start, End: cur[len(cur)-1].End})
			cur, chars = nil, 0
		}
	}
	for i, w := range words {
		length := utf8.RuneCountInString(w.Text)
		if len(cur) > 0 {
			tooLong := chars+1+length > lim.MaxChars ||
				lim.MaxWords > 0 && len(cur) >= lim.MaxWords ||
				w.End-cur[0].Start > lim.MaxSeconds
			if tooLong || w.Start-words[i-1].End >= lim.BreakPause {
				flush()
			}
		}
		if len(cur) > 0 {
			chars++
		}
		cur = append(cur, w)
		chars += length
		if endsSentence(w.Text) {
			flush()
		}
	}
	flush()
	// Never let a cue overlap the next one.
	for i := 0; i+1 < len(cues); i++ {
		cues[i].End = math.Min(cues[i].End, cues[i+1].Start)
	}
	return cues
}

// SRT renders cues as an SubRip file, with times relative to offset (the sermon's start).
func SRT(cues []Cue, offset float64) string {
	var b strings.Builder
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1,
			timeline.SRT(c.Start-offset), timeline.SRT(c.End-offset), c.Text())
	}
	return b.String()
}

// Paragraphs renders words as readable paragraphs, breaking at long pauses or after about
// 120 words at the next sentence end.
func Paragraphs(words []Word) string {
	var paras []string
	start := 0
	for i := range words {
		last := i == len(words)-1
		longPause := !last && words[i+1].Start-words[i].End > 2.5
		longEnough := i-start >= 120 && endsSentence(words[i].Text)
		if last || longPause || longEnough {
			paras = append(paras, Join(words[start:i+1]))
			start = i + 1
		}
	}
	return strings.Join(paras, "\n\n") + "\n"
}
