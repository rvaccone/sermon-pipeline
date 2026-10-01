// Package cut places edit points inside pauses, so a cut never clips the start or end of a word.
// The probe found that word timings alone put cuts a syllable too late or early.
package cut

import (
	"context"
	"math"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// Pauses finds silent stretches within a span of the recording.
type Pauses func(ctx context.Context, within timeline.Span) ([]timeline.Span, error)

const (
	reach = 0.75 // furthest a cut may move from the word boundary to find a pause
	lead  = 0.3  // silence kept before the first word
	tail  = 0.5  // silence kept after the last word
	guard = 0.05 // margin kept clear of a neighboring word
)

// Words cuts around words[first..last]: just before the first word starts and just after the
// last one ends, each inside a pause when one is close enough. A cut never crosses into the
// neighboring words, so it can't pull in the end of the previous sentence or the start of the
// next. found is false when either end had no pause within reach and fell back to word timing.
func Words(ctx context.Context, pauses Pauses, words []transcript.Word, first, last int) (span timeline.Span, found bool, err error) {
	start, end := words[first].Start, words[last].End
	floor := math.Max(start-reach, start-lead-1)
	if first > 0 {
		floor = math.Max(floor, words[first-1].End+guard)
	}
	ceiling := end + reach
	if last+1 < len(words) {
		ceiling = math.Min(ceiling, words[last+1].Start-guard)
	}

	before, foundBefore, err := cutBefore(ctx, pauses, start, math.Min(floor, start))
	if err != nil {
		return span, false, err
	}
	after, foundAfter, err := cutAfter(ctx, pauses, end, math.Max(ceiling, end))
	if err != nil {
		return span, false, err
	}
	return timeline.Span{Start: before, End: after}, foundBefore && foundAfter, nil
}

// cutBefore returns a point in [floor, t]: inside the pause that ends closest to t, leaving a
// short lead of silence, or a lead before t (bounded by floor) when there is no pause.
func cutBefore(ctx context.Context, pauses Pauses, t, floor float64) (float64, bool, error) {
	found, err := pauses(ctx, timeline.Span{Start: floor, End: t})
	if err != nil {
		return 0, false, err
	}
	best, ok, bestDist := math.Max(floor, t-lead), false, math.Inf(1)
	for _, p := range found {
		if d := math.Abs(p.End - t); d < bestDist {
			best, ok, bestDist = math.Max(floor, math.Max(p.Start, math.Min(t, p.End)-lead)), true, d
		}
	}
	return best, ok, nil
}

// cutAfter returns a point in [t, ceiling]: inside the pause that starts closest to t, keeping a
// short tail of silence, or a tail after t (bounded by ceiling) when there is no pause.
func cutAfter(ctx context.Context, pauses Pauses, t, ceiling float64) (float64, bool, error) {
	found, err := pauses(ctx, timeline.Span{Start: t, End: ceiling})
	if err != nil {
		return 0, false, err
	}
	best, ok, bestDist := math.Min(ceiling, t+tail), false, math.Inf(1)
	for _, p := range found {
		if d := math.Abs(p.Start - t); d < bestDist {
			start := math.Max(t, p.Start)
			best, ok, bestDist = math.Min(ceiling, start+math.Min(tail, (p.End-start)/2)), true, d
		}
	}
	return best, ok, nil
}
