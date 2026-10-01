// Package terms fixes mis-heard names and Bible references in the transcript. Claude proposes
// corrections; only those that pass Allowed are applied. A correction must sound like what was
// transcribed and change nothing else, so the preacher's words are never rewritten.
package terms

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

type answer struct {
	Corrections []transcript.Correction `json:"corrections"`
}

// Correct proposes corrections for the words inside span and applies the allowed ones to the
// whole transcript.
func Correct(ctx context.Context, ask llm.Asker, t transcript.Transcript, span timeline.Span, glossary []string) (transcript.Transcript, []transcript.Outcome, error) {
	input := fmt.Sprintf("Glossary:\n- %s\n\nTranscript:\n%s",
		strings.Join(glossary, "\n- "), transcript.Lines(t.Within(span), nil, ""))
	var a answer
	if err := ask.Ask(ctx, llm.Load("transcript-corrections"), input, &a); err != nil {
		return t, nil, err
	}
	fixed, outcomes := t.Apply(a.Corrections, Allowed(glossary))
	return fixed, outcomes, nil
}

// maxDifference is how different (as a share of length) the changed words may be from what was
// transcribed: enough for "parts" → "Hearts" (0.33), too little for "party" → "Art" (0.4).
const maxDifference = 0.35

// Allowed returns the rule every correction must pass: it is a well-formed Bible reference
// whose book and numbers were actually spoken, or it substitutes a glossary term for words that
// sound like it and changes nothing else.
func Allowed(glossary []string) func(transcript.Correction) (bool, string) {
	terms := make([]*regexp.Regexp, len(glossary))
	for i, term := range glossary {
		terms[i] = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(term) + `\b`)
	}
	return func(c transcript.Correction) (bool, string) {
		switch {
		case strings.TrimSpace(c.Replace) == "" || c.Find == c.Replace:
			return false, "no change"
		case reference.MatchString(c.Replace):
			return checkReference(c)
		default:
			return checkGlossary(c, terms)
		}
	}
}

// checkGlossary allows a replacement that uses whole glossary terms, sounds like the transcribed
// words, and changes no other word.
func checkGlossary(c transcript.Correction, terms []*regexp.Regexp) (bool, string) {
	rest, used := c.Replace, false
	for _, pattern := range terms {
		if pattern.MatchString(rest) {
			rest, used = pattern.ReplaceAllString(rest, " "), true
		}
	}
	if !used {
		return false, "replacement is neither a glossary term nor a Bible reference"
	}
	heard, written := changedWords(c.Find, c.Replace)
	if heard != written && !similar(heard, written) {
		return false, "replacement does not sound like the transcribed words"
	}
	spoken := map[string]bool{}
	for _, w := range strings.Fields(c.Find) {
		spoken[squash(w)] = true
	}
	for _, w := range strings.Fields(rest) {
		if s := squash(w); s != "" && !spoken[s] {
			return false, "replacement also changes the word " + w
		}
	}
	return true, ""
}

// changedWords returns, squashed, the words only in find and the words only in replace: the part
// of the phrase the correction actually changes.
func changedWords(find, replace string) (heard, written string) {
	inFind, inReplace := map[string]bool{}, map[string]bool{}
	for _, w := range strings.Fields(find) {
		inFind[squash(w)] = true
	}
	for _, w := range strings.Fields(replace) {
		inReplace[squash(w)] = true
	}
	for _, w := range strings.Fields(find) {
		if !inReplace[squash(w)] {
			heard += squash(w)
		}
	}
	for _, w := range strings.Fields(replace) {
		if !inFind[squash(w)] {
			written += squash(w)
		}
	}
	return heard, written
}

// reference matches "Matthew 24:14", "1 Peter 2:4-10", "Song of Solomon 2", with optional
// trailing punctuation.
var reference = regexp.MustCompile(`^(?:([1-3]) )?([A-Z][a-z]+(?: of [A-Z][a-z]+)?) (\d{1,3})(?::(\d{1,3})(?:[-–](\d{1,3}))?)?[.,;:!?]?$`)

func checkReference(c transcript.Correction) (bool, string) {
	m := reference.FindStringSubmatch(c.Replace)
	book := m[2]
	if !books[book] {
		return false, book + " is not a book of the Bible"
	}
	if !resemblesAWord(squash(book), c.Find) {
		return false, "the book " + book + " was not said"
	}
	spoken := spokenNumbers(c.Find)
	for _, n := range []string{m[1], m[3], m[4], m[5]} {
		if v, err := strconv.Atoi(n); err == nil && !spoken[v] {
			return false, fmt.Sprintf("the number %d was not said", v)
		}
	}
	return true, ""
}

// resemblesAWord reports whether squashed text is similar to one word of phrase, or two
// adjacent words (for "Song of Solomon"-style names split by the transcriber).
func resemblesAWord(target, phrase string) bool {
	words := strings.Fields(phrase)
	for i := range words {
		for j := i + 1; j <= min(i+3, len(words)); j++ {
			if similar(target, squash(strings.Join(words[i:j], ""))) {
				return true
			}
		}
	}
	return false
}

// similar reports whether two squashed strings differ by at most maxDifference of the longer.
func similar(a, b string) bool {
	longest := max(len([]rune(a)), len([]rune(b)))
	return longest > 0 && float64(editDistance(a, b)) <= maxDifference*float64(longest)
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

var digits = regexp.MustCompile(`\d+`)

// spokenNumbers collects the numbers in a phrase, written as digits or words ("twenty four",
// "first").
func spokenNumbers(phrase string) map[int]bool {
	found := map[int]bool{}
	for _, d := range digits.FindAllString(phrase, -1) {
		n, _ := strconv.Atoi(d)
		found[n] = true
	}
	pending := 0
	flush := func() {
		if pending > 0 {
			found[pending] = true
			pending = 0
		}
	}
	for _, w := range strings.Fields(strings.ToLower(phrase)) {
		w = strings.TrimFunc(w, unicode.IsPunct)
		switch v, ok := numberWords[w]; {
		case !ok:
			flush()
		case v >= 20 && v%10 == 0:
			flush()
			pending = v
		case pending >= 20 && v < 10:
			pending += v
			flush()
		default:
			flush()
			found[v] = true
		}
	}
	flush()
	return found
}

var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
	"ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16,
	"seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90, "first": 1, "second": 2, "third": 3,
}

var books = map[string]bool{}

func init() {
	for _, b := range strings.Split("Genesis Exodus Leviticus Numbers Deuteronomy Joshua Judges Ruth Samuel Kings "+
		"Chronicles Ezra Nehemiah Esther Job Psalm Psalms Proverbs Ecclesiastes Isaiah Jeremiah Lamentations Ezekiel "+
		"Daniel Hosea Joel Amos Obadiah Jonah Micah Nahum Habakkuk Zephaniah Haggai Zechariah Malachi Matthew Mark "+
		"Luke John Acts Romans Corinthians Galatians Ephesians Philippians Colossians Thessalonians Timothy Titus "+
		"Philemon Hebrews James Peter Jude Revelation", " ") {
		books[b] = true
	}
	books["Song of Solomon"], books["Song of Songs"] = true, true
}

// squash lowercases and drops everything but letters and digits.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
