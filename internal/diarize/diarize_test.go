package diarize

import (
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

func TestParseAndClean(t *testing.T) {
	out := []byte(`Started
0.031 -- 6.325 speaker_00
6.900 -- 16.619 speaker_00
16.700 -- 17.100 speaker_01
17.200 -- 40.000 speaker_02
300.000 -- 320.000 speaker_02
Elapsed seconds: 74`)
	turns := Clean(parse(out))
	if len(turns) != 3 {
		t.Fatalf("turns = %+v; want the blip dropped, the close turns merged, the far ones kept apart", turns)
	}
	if turns[0].Speaker != "speaker_00" || turns[0].End != 16.619 || turns[1].End != 40 || turns[2].Start != 300 {
		t.Errorf("turns = %+v", turns)
	}
}

func TestCleanKeepsAFragmentedVoice(t *testing.T) {
	var turns []Turn
	for i := 0; i < 5; i++ {
		start := float64(i) * 1.6
		turns = append(turns, Turn{timeline.Span{Start: start, End: start + 1.5}, "a"})
	}
	if got := Clean(turns); len(got) != 1 || got[0].Duration() < 7 {
		t.Errorf("Clean = %+v; 1.5 s pieces 0.1 s apart are one turn", got)
	}
}

func TestMainAndOthers(t *testing.T) {
	turns := []Turn{
		{timeline.Span{Start: 0, End: 100}, "a"},
		{timeline.Span{Start: 100, End: 130}, "b"},
		{timeline.Span{Start: 130, End: 300}, "a"},
	}
	span := timeline.Span{Start: 50, End: 200}
	if Main(turns, span) != "a" {
		t.Error("Main should pick the speaker with the most talk time")
	}
	if others := Others(turns, span, "a"); len(others) != 1 || others[0].Speaker != "b" {
		t.Errorf("Others = %+v", others)
	}
	label := WithPreacher(turns, "a")
	if label(10) != "Preacher" || label(110) != "Speaker 2" || label(500) != Unclear {
		t.Error("WithPreacher")
	}
	neutral := Neutral(turns)
	if neutral(10) != "Speaker 1" || neutral(110) != "Speaker 2" {
		t.Error("Neutral should number speakers by talk time")
	}
	if WithPreacher(nil, "")(5) != Unclear {
		t.Error("an unknown preacher must not label silence as Preacher")
	}
}
