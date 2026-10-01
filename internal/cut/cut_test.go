package cut

import (
	"context"
	"math"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

func fixed(spans ...timeline.Span) Pauses {
	return func(_ context.Context, within timeline.Span) ([]timeline.Span, error) {
		var out []timeline.Span
		for _, s := range spans {
			if s.Overlap(within) > 0 {
				out = append(out, s)
			}
		}
		return out, nil
	}
}

// sentence: "before | One two | after" with words one second apart.
var sentence = []transcript.Word{
	{Text: "before.", Start: 8, End: 8.6},
	{Text: "One", Start: 10, End: 10.3},
	{Text: "two.", Start: 11, End: 11.4},
	{Text: "after", Start: 13, End: 13.4},
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCutsLandInPauses(t *testing.T) {
	pauses := fixed(timeline.Span{Start: 9, End: 9.9}, timeline.Span{Start: 11.5, End: 12.4})
	span, found, err := Words(context.Background(), pauses, sentence, 1, 2)
	if err != nil || !found {
		t.Fatalf("span %+v found %v err %v", span, found, err)
	}
	if !near(span.Start, 9.6) || !near(span.End, 11.95) {
		t.Errorf("span = %+v; want 9.6 (lead inside the pause) .. 11.95 (half of the short pause)", span)
	}
}

func TestCutsNeverCrossIntoNeighbors(t *testing.T) {
	// Continuous speech: the neighbors are close and the only pauses are beyond them.
	words := []transcript.Word{
		{Text: "end", Start: 9.5, End: 9.9},
		{Text: "One", Start: 10, End: 10.3},
		{Text: "two.", Start: 11, End: 11.4},
		{Text: "Next", Start: 11.5, End: 11.8},
	}
	pauses := fixed(timeline.Span{Start: 8, End: 9.4}, timeline.Span{Start: 12, End: 13})
	span, found, _ := Words(context.Background(), pauses, words, 1, 2)
	if found {
		t.Error("no pause lies between the clip and its neighbors")
	}
	if span.Start < 9.9 || span.End > 11.5 {
		t.Errorf("span = %+v crosses into a neighboring word", span)
	}
}

func TestCutsStayWithinReach(t *testing.T) {
	pauses := fixed(timeline.Span{Start: 6, End: 7}) // 3 s before the word: too far
	span, found, _ := Words(context.Background(), pauses, sentence, 1, 2)
	if found || span.Start < 10-reach-1e-9 {
		t.Errorf("span = %+v found %v; a distant pause must not be used", span, found)
	}
}
