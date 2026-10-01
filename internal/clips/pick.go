// Package clips picks, frames and renders short vertical clips of the sermon.
package clips

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/cut"
	"github.com/rvaccone/sermon-pipeline/internal/diarize"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/slug"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// Clip is a chosen moment, on the source timeline, with its post text.
type Clip struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Caption string        `json:"caption"`
	Span    timeline.Span `json:"span"`
	Score   int           `json:"score"`
	Review  string        `json:"review"` // the second reviewer's reasoning
}

// Rejection records a candidate that did not make it, and why.
type Rejection struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// Selection is the outcome of picking.
type Selection struct {
	Clips    []Clip      `json:"clips"`
	Rejected []Rejection `json:"rejected"`
}

// PickInput is everything Pick reads.
type PickInput struct {
	Transcript transcript.Transcript
	Sermon     timeline.Span
	Turns      []diarize.Turn
	Preacher   string
	Count      int
	MinScore   int // the reviewer's score a clip needs to be kept
	MinSeconds float64
	MaxSeconds float64
	Pauses     cut.Pauses
}

type candidateAnswer struct {
	Candidates []struct {
		StartQuote string `json:"start_quote"`
		StartTime  string `json:"start_time"`
		EndQuote   string `json:"end_quote"`
		EndTime    string `json:"end_time"`
		Title      string `json:"title"`
		Caption    string `json:"caption"`
	} `json:"candidates"`
}

type reviewAnswer struct {
	Reviews []struct {
		Number  int    `json:"number" jsonschema_description:"The candidate's number from the input"`
		Verdict string `json:"verdict" jsonschema:"enum=keep,enum=reject"`
		Reason  string `json:"reason" jsonschema_description:"One sentence"`
		Score   int    `json:"score" jsonschema_description:"From 1 to 10: how strong it is as a standalone clip"`
	} `json:"reviews"`
}

// candidate is a proposal that passed the mechanical checks and awaits review.
type candidate struct {
	Clip
	words  []transcript.Word // exactly what the clip will contain
	before []transcript.Word // the sentence leading into it, for the reviewer's context
}

// extra candidates are requested beyond Count, since checks and review reject some.
const extra = 5

// Pick proposes candidates, cuts each into pauses and checks the result against the transcript
// and speaker turns, has a second pass review exactly what will be rendered, and keeps the best
// non-overlapping clips.
func Pick(ctx context.Context, ask llm.Asker, in PickInput) (Selection, error) {
	var sel Selection
	if in.Count == 0 {
		return sel, nil
	}
	label := diarize.WithPreacher(in.Turns, in.Preacher)
	var proposed candidateAnswer
	request := fmt.Sprintf("Choose %d candidates, each %.0f-%.0f seconds long.\n\nTranscript:\n%s",
		in.Count+extra, in.MinSeconds, in.MaxSeconds, transcript.Lines(in.Transcript.Within(in.Sermon), label, diarize.Unclear))
	if err := ask.Ask(ctx, llm.Load("clip-candidates"), request, &proposed); err != nil {
		return sel, err
	}

	var candidates []candidate
	for _, p := range proposed.Candidates {
		c, reason, err := check(ctx, in, p.StartQuote, p.StartTime, p.EndQuote, p.EndTime)
		if err != nil {
			return sel, err
		}
		if reason != "" {
			sel.Rejected = append(sel.Rejected, Rejection{Title: p.Title, Reason: reason})
			continue
		}
		c.Title, c.Caption = p.Title, p.Caption
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 {
		return sel, nil
	}

	var reviewed reviewAnswer
	if err := ask.Ask(ctx, llm.Load("clip-review"), reviewRequest(candidates, label), &reviewed); err != nil {
		return sel, err
	}
	seen := map[int]bool{}
	var kept []candidate
	for _, r := range reviewed.Reviews {
		if r.Number < 1 || r.Number > len(candidates) || seen[r.Number] {
			continue
		}
		seen[r.Number] = true
		c := candidates[r.Number-1]
		if r.Verdict != "keep" {
			sel.Rejected = append(sel.Rejected, Rejection{Title: c.Title, Reason: "reviewer: " + r.Reason})
			continue
		}
		c.Score, c.Review = min(10, max(1, r.Score)), r.Reason
		if c.Score < in.MinScore {
			sel.Rejected = append(sel.Rejected, Rejection{Title: c.Title, Reason: fmt.Sprintf("scored %d/10: %s", c.Score, r.Reason)})
			continue
		}
		kept = append(kept, c)
	}
	for i, c := range candidates {
		if !seen[i+1] {
			sel.Rejected = append(sel.Rejected, Rejection{Title: c.Title, Reason: "the reviewer did not assess it"})
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Score > kept[j].Score })

	for _, c := range kept {
		if len(sel.Clips) == in.Count {
			break
		}
		if overlapsAny(c.Span, sel.Clips) {
			sel.Rejected = append(sel.Rejected, Rejection{Title: c.Title, Reason: "overlaps a stronger clip"})
			continue
		}
		c.ID = fmt.Sprintf("%02d-%s", len(sel.Clips)+1, slug.Make(c.Title, 40))
		sel.Clips = append(sel.Clips, c.Clip)
	}
	return sel, nil
}

// check locates a proposal's quotes, cuts the clip into pauses, and applies the mechanical rules
// to the final cut. It returns a reason when the proposal fails.
func check(ctx context.Context, in PickInput, startQuote, startTime, endQuote, endTime string) (candidate, string, error) {
	find := func(quote, at string) (transcript.Match, bool) {
		near, err := timeline.Parse(at)
		if err != nil {
			return transcript.Match{}, false
		}
		return in.Transcript.Find(quote, near, 30)
	}
	first, ok1 := find(startQuote, startTime)
	last, ok2 := find(endQuote, endTime)
	switch {
	case !ok1 || !ok2:
		return candidate{}, "its quotes are not in the transcript", nil
	case last.Last < first.First:
		return candidate{}, "its end comes before its start", nil
	}
	words := in.Transcript.Words
	span, _, err := cut.Words(ctx, in.Pauses, words, first.First, last.Last)
	if err != nil {
		return candidate{}, "", err
	}
	switch {
	case span.Start < in.Sermon.Start || span.End > in.Sermon.End:
		return candidate{}, "it falls outside the sermon", nil
	case span.Duration() < in.MinSeconds || span.Duration() > in.MaxSeconds:
		return candidate{}, fmt.Sprintf("it is %.0f seconds long", span.Duration()), nil
	}
	for _, t := range diarize.Others(in.Turns, span, in.Preacher) {
		if t.Overlap(span) > 1 {
			return candidate{}, "someone other than the preacher speaks in it", nil
		}
	}
	return candidate{
		Clip:   Clip{Span: span},
		words:  words[first.First : last.Last+1],
		before: words[max(0, first.First-25):first.First],
	}, "", nil
}

// reviewRequest shows the reviewer exactly what each clip contains, labeled by speaker, with the
// lead-in for context and the caption that would be posted.
func reviewRequest(candidates []candidate, label func(float64) string) string {
	var b strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&b, "Candidate %d: %q (%.0f seconds)\n", i+1, c.Title, c.Span.Duration())
		if len(c.before) > 0 {
			fmt.Fprintf(&b, "Just before the clip: …%s\n", transcript.Join(c.before))
		}
		fmt.Fprintf(&b, "Clip:\n%sCaption: %s\n\n", transcript.Lines(c.words, label, diarize.Unclear), c.Caption)
	}
	return b.String()
}

func overlapsAny(span timeline.Span, clips []Clip) bool {
	for _, c := range clips {
		if c.Span.Overlap(span) > 0 {
			return true
		}
	}
	return false
}
