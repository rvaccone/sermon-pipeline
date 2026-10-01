package transcript

import (
	"sort"
	"strings"
	"unicode"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// correctionWindow is how far from its stated time a correction's words may be found. The nearest
// occurrence wins, so a generous window costs nothing.
const correctionWindow = 30

// Correction replaces a mis-heard phrase at a given time, e.g. "Healing Parts" → "Healing Hearts".
type Correction struct {
	Find    string `json:"find" jsonschema_description:"The words exactly as they appear in the transcript, punctuation included"`
	Replace string `json:"replace" jsonschema_description:"The corrected words, keeping the original punctuation"`
	Time    string `json:"time" jsonschema_description:"The time printed at the start of the line where the words appear, e.g. 24:41"`
	Reason  string `json:"reason" jsonschema_description:"Briefly, why this is a mis-hearing"`
}

// Outcome records whether a correction was applied, and why not if it wasn't.
type Outcome struct {
	Correction
	Applied bool   `json:"applied"`
	Skipped string `json:"skipped,omitempty"`
}

// Apply makes the allowed corrections and returns a new transcript. Replacement words share the
// original words' time span in proportion to their length, so captions stay in sync.
func (t Transcript) Apply(corrections []Correction, allowed func(Correction) (bool, string)) (Transcript, []Outcome) {
	type located struct {
		Match
		c Correction
	}
	var outcomes []Outcome
	var edits []located
	for _, c := range corrections {
		if ok, why := allowed(c); !ok {
			outcomes = append(outcomes, Outcome{Correction: c, Skipped: why})
			continue
		}
		near, err := timeline.Parse(c.Time)
		if err != nil {
			outcomes = append(outcomes, Outcome{Correction: c, Skipped: "unreadable time " + c.Time})
			continue
		}
		m, ok := t.Find(c.Find, near, correctionWindow)
		if !ok {
			outcomes = append(outcomes, Outcome{Correction: c, Skipped: "words not found near that time"})
			continue
		}
		edits = append(edits, located{m, c})
	}

	sort.Slice(edits, func(i, j int) bool { return edits[i].First < edits[j].First })
	words := make([]Word, 0, len(t.Words))
	next := 0
	for _, e := range edits {
		if e.First < next {
			outcomes = append(outcomes, Outcome{Correction: e.c, Skipped: "overlaps another correction"})
			continue
		}
		words = append(words, t.Words[next:e.First]...)
		replacement := keepPunctuation(strings.Fields(e.c.Replace), t.Words[e.First].Text, t.Words[e.Last].Text)
		words = append(words, retime(replacement, t.Words[e.First].Start, t.Words[e.Last].End)...)
		next = e.Last + 1
		outcomes = append(outcomes, Outcome{Correction: e.c, Applied: true})
	}
	words = append(words, t.Words[next:]...)
	return Transcript{Words: words}, outcomes
}

// retime spreads texts across [start, end] in proportion to their lengths.
func retime(texts []string, start, end float64) []Word {
	total := 0
	for _, s := range texts {
		total += len(s)
	}
	words := make([]Word, len(texts))
	at := start
	for i, s := range texts {
		d := (end - start) * float64(len(s)) / float64(max(total, 1))
		words[i] = Word{Text: s, Start: at, End: at + d}
		at += d
	}
	return words
}

// keepPunctuation carries the original phrase's leading and trailing punctuation onto the
// replacement when it lacks them, so a corrected word doesn't lose the period that ends its
// sentence (which caption and paragraph breaks depend on).
func keepPunctuation(replacement []string, first, last string) []string {
	if len(replacement) == 0 {
		return replacement
	}
	isPunct := func(r rune) bool { return unicode.IsPunct(r) }
	lead := first[:len(first)-len(strings.TrimLeftFunc(first, isPunct))]
	trail := last[len(strings.TrimRightFunc(last, isPunct)):]
	out := append([]string(nil), replacement...)
	if lead != "" && strings.TrimLeftFunc(out[0], isPunct) == out[0] {
		out[0] = lead + out[0]
	}
	if n := len(out) - 1; trail != "" && strings.TrimRightFunc(out[n], isPunct) == out[n] {
		out[n] += trail
	}
	return out
}
