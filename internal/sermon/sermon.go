// Package sermon finds where the sermon begins and ends. Claude proposes the boundaries with
// quoted evidence; this package locates the quotes, snaps the cuts into pauses, and cross-checks
// the result against speaker detection, flagging anything a person should look at.
package sermon

import (
	"context"
	"fmt"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/cut"
	"github.com/rvaccone/sermon-pipeline/internal/diarize"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// Boundary is the sermon's span on the source timeline, with the evidence behind it.
type Boundary struct {
	Span         timeline.Span `json:"span"`
	Source       string        `json:"source"` // "claude" or "override"
	StartQuote   string        `json:"start_quote,omitempty"`
	EndQuote     string        `json:"end_quote,omitempty"`
	Confidence   string        `json:"confidence,omitempty"`
	Reasoning    string        `json:"reasoning,omitempty"`
	Preacher     string        `json:"preacher_speaker"`        // diarization label of the preacher
	PreacherName string        `json:"preacher_name,omitempty"` // if the transcript names them
	Flags        []string      `json:"flags,omitempty"`         // things a person should check
}

// Input is everything Detect reads.
type Input struct {
	Transcript transcript.Transcript
	Turns      []diarize.Turn
	Ends       string // config: "after-closing-prayer" or "after-teaching"
	Duration   float64
	Pauses     cut.Pauses
}

// answer is the shape Claude must return.
type answer struct {
	StartQuote   string   `json:"start_quote" jsonschema_description:"6-15 consecutive words copied exactly from the transcript: the first words of the sermon"`
	StartTime    string   `json:"start_time" jsonschema_description:"The time printed on the line where start_quote begins, e.g. 24:41"`
	EndQuote     string   `json:"end_quote" jsonschema_description:"6-15 consecutive words copied exactly from the transcript: the last words of the sermon"`
	EndTime      string   `json:"end_time" jsonschema_description:"The time printed on the line where end_quote begins, e.g. 1:22:20"`
	PreacherName string   `json:"preacher_name" jsonschema_description:"The preacher's name, only if the transcript states it; otherwise an empty string"`
	Confidence   string   `json:"confidence" jsonschema:"enum=high,enum=medium,enum=low"`
	Reasoning    string   `json:"reasoning" jsonschema_description:"Two or three sentences on how the boundaries were identified"`
	Notes        []string `json:"notes" jsonschema_description:"Anything unusual about this service; empty if nothing"`
}

// Detect asks Claude for the boundaries, then verifies and refines them.
func Detect(ctx context.Context, ask llm.Asker, in Input) (Boundary, error) {
	prompt := fmt.Sprintf("Rule for the end of the sermon: %s\n\nTranscript:\n%s",
		in.Ends, transcript.Lines(in.Transcript.Words, diarize.Neutral(in.Turns), diarize.Unclear))
	var a answer
	if err := ask.Ask(ctx, llm.Load("sermon-boundaries"), prompt, &a); err != nil {
		return Boundary{}, err
	}

	b := Boundary{
		Source: "claude", StartQuote: a.StartQuote, EndQuote: a.EndQuote,
		Confidence: a.Confidence, Reasoning: a.Reasoning,
	}
	for _, note := range a.Notes {
		b.flag("Claude noted: " + note)
	}
	if a.Confidence != "high" {
		b.flag(fmt.Sprintf("Claude's confidence in the boundaries is %s.", a.Confidence))
	}

	first, err := locate(in.Transcript, a.StartQuote, a.StartTime)
	if err != nil {
		return b, err
	}
	last, err := locate(in.Transcript, a.EndQuote, a.EndTime)
	if err != nil {
		return b, err
	}
	if last.Last <= first.First {
		return b, fmt.Errorf("Claude's sermon end (%s) is not after its start (%s)", a.EndTime, a.StartTime)
	}
	span, inPauses, err := cut.Words(ctx, in.Pauses, in.Transcript.Words, first.First, last.Last)
	if err != nil {
		return b, err
	}
	if !inPauses {
		b.flag("A sermon cut point has no pause nearby; it was placed by word timing. Listen to the first and last seconds.")
	}
	b.Span = span
	b.PreacherName = b.verifiedName(a.PreacherName, in.Transcript)
	b.crossCheck(in.Turns)
	return b, nil
}

// FromOverride builds a boundary from times a person chose. They are used exactly as given.
func FromOverride(span timeline.Span, turns []diarize.Turn) Boundary {
	b := Boundary{Span: span, Source: "override"}
	b.crossCheck(turns)
	return b
}

// locate finds a quote near the time Claude gave for it.
func locate(t transcript.Transcript, quote, at string) (transcript.Match, error) {
	near, err := timeline.Parse(at)
	if err != nil {
		return transcript.Match{}, fmt.Errorf("Claude gave an unreadable time %q: %w", at, err)
	}
	m, ok := t.Find(quote, near, 45)
	if !ok {
		return m, fmt.Errorf("Claude's quote %q is not in the transcript near %s; rerun with --start/--end", quote, at)
	}
	return m, nil
}

// verifiedName keeps Claude's preacher name only if the name itself (without a title like
// "Pastor") appears in the sermon's transcript, so an invented name can't reach a description.
func (b *Boundary) verifiedName(name string, t transcript.Transcript) string {
	words := strings.Fields(name)
	for len(words) > 1 && titles[strings.ToLower(strings.TrimSuffix(words[0], "."))] {
		words = words[1:]
	}
	if len(words) == 0 {
		return ""
	}
	middle := (b.Span.Start + b.Span.End) / 2
	if _, ok := t.Find(strings.Join(words, " "), middle, b.Span.Duration()); ok {
		return name
	}
	b.flag(fmt.Sprintf("Claude named the preacher %q, but the name isn't in the transcript, so it was not used.", name))
	return ""
}

var titles = map[string]bool{"pastor": true, "brother": true, "sister": true, "rev": true, "reverend": true, "dr": true, "elder": true}

// crossCheck compares the boundaries with speaker detection and typical sermon length.
func (b *Boundary) crossCheck(turns []diarize.Turn) {
	b.Preacher = diarize.Main(turns, b.Span)
	if minutes := b.Span.Duration() / 60; minutes < 15 || minutes > 80 {
		b.flag(fmt.Sprintf("The sermon is %.0f minutes long, which is unusual.", minutes))
	}
	edges := []struct {
		name string
		at   float64
	}{{"starts", b.Span.Start + 2}, {"ends", b.Span.End - 2}}
	for _, e := range edges {
		if who := diarize.At(turns, e.at); who != "" && who != b.Preacher {
			b.flag(fmt.Sprintf("The sermon %s while speaker detection hears someone other than the preacher (at %s).",
				e.name, timeline.Clock(e.at)))
		}
	}
	var others []string
	for _, t := range diarize.Others(turns, b.Span, b.Preacher) {
		if t.Duration() >= 30 {
			others = append(others, t.Span.String())
		}
	}
	if len(others) > 0 {
		b.flag("Someone other than the preacher speaks during the sermon (" + strings.Join(others, ", ") +
			"), e.g. a testimony or reading. Clips avoid these stretches.")
	}
}

func (b *Boundary) flag(msg string) { b.Flags = append(b.Flags, msg) }
