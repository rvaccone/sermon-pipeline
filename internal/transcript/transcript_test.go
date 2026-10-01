package transcript

import (
	"strings"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

func words(text string, start, step float64) []Word {
	var out []Word
	for i, s := range strings.Fields(text) {
		t := start + float64(i)*step
		out = append(out, Word{Text: s, Start: t, End: t + step*0.8})
	}
	return out
}

func TestParseWhisperJSON(t *testing.T) {
	data := `{"transcription":[{"tokens":[
		{"text":"[_BEG_]","t_dtw":-1,"offsets":{"from":0}},
		{"text":" Good","t_dtw":10,"offsets":{"from":100}},
		{"text":" morn","t_dtw":40,"offsets":{"from":400}},
		{"text":"ing,","t_dtw":60,"offsets":{"from":600}},
		{"text":" -","t_dtw":200,"offsets":{"from":2000}},
		{"text":" church.","t_dtw":250,"offsets":{"from":2500}},
		{"text":"[_TT_150]","t_dtw":-1,"offsets":{"from":3000}}]}]}`
	got, err := parseWhisperJSON([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if text := Join(got.Words); text != "Good morning, church." {
		t.Fatalf("text = %q", text)
	}
	morning := got.Words[1]
	if morning.Start != 0.4 || morning.End >= 2.5 {
		t.Errorf("morning = %+v; its end should be estimated, leaving a pause before 2.5", morning)
	}
}

func TestChunkMergesCloseRegionsAndPads(t *testing.T) {
	regions := []timeline.Span{{Start: 10, End: 20}, {Start: 22, End: 30}, {Start: 100, End: 110}}
	got := chunk(regions)
	want := []timeline.Span{{Start: 9.7, End: 30.3}, {Start: 99.7, End: 110.3}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("chunk = %+v; want %+v", got, want)
	}
}

func TestPausesSurviveIntoCues(t *testing.T) {
	// A 0.8 s pause mid-sentence (start-to-start gap 1.1 s) must break the caption.
	data := `{"transcription":[{"tokens":[
		{"text":" When","t_dtw":0,"offsets":{"from":0}},
		{"text":" you","t_dtw":30,"offsets":{"from":300}},
		{"text":" pray","t_dtw":60,"offsets":{"from":600}},
		{"text":" do","t_dtw":170,"offsets":{"from":1700}},
		{"text":" this.","t_dtw":190,"offsets":{"from":1900}}]}]}`
	tr, err := parseWhisperJSON([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	cues := Cues(tr.Words, YouTubeLimits)
	if len(cues) != 2 || cues[0].Text() != "When you pray" {
		t.Errorf("cues = %+v; want a break at the pause", cues)
	}
}

func TestFind(t *testing.T) {
	tr := Transcript{Words: words("turn to Matthew 24, 14 today and Matthew 24, 14 again", 0, 1)}
	m, ok := tr.Find("matthew 24:14", 8, 20)
	if !ok || m.First != 7 || m.Last != 9 {
		t.Errorf("Find near 8 = %+v %v; want the second occurrence 7..9", m, ok)
	}
	if _, ok := tr.Find("Matthew 24:14", 100, 5); ok {
		t.Error("Find should respect the window")
	}
	if _, ok := tr.Find("Mark 3", 0, 100); ok {
		t.Error("Find should not match absent phrases")
	}
}

func TestApplyCorrections(t *testing.T) {
	tr := Transcript{Words: words("the local Amman the religious leader", 0, 1)}
	allowAll := func(Correction) (bool, string) { return true, "" }
	fixed, outcomes := tr.Apply([]Correction{
		{Find: "Amman", Replace: "imam", Time: "0:02"},
		{Find: "nonexistent", Replace: "x", Time: "0:02"},
	}, allowAll)
	if Join(fixed.Words) != "the local imam the religious leader" {
		t.Errorf("got %q", Join(fixed.Words))
	}
	if !outcomes[1].Applied || outcomes[0].Applied {
		t.Errorf("outcomes = %+v", outcomes)
	}
	if fixed.Words[2].Start != 2 {
		t.Errorf("replacement should keep the original timing, got %+v", fixed.Words[2])
	}
}

func TestCuesBreakAtSentencesAndPauses(t *testing.T) {
	ws := words("One two three. Four five", 0, 0.5)
	ws[3].Start, ws[3].End = 10, 10.4 // long pause before "Four"
	ws[4].Start, ws[4].End = 10.5, 10.9
	cues := Cues(ws, YouTubeLimits)
	if len(cues) != 2 || cues[0].Text() != "One two three." || cues[1].Text() != "Four five" {
		t.Fatalf("cues = %+v", cues)
	}
	srt := SRT(cues, 0)
	if !strings.HasPrefix(srt, "1\n00:00:00,000 --> ") {
		t.Errorf("srt = %q", srt)
	}
}

func TestLinesLabelsSpeakers(t *testing.T) {
	ws := words("Have a blessed Sunday. Amen. It's good to be here, is it not?", 0, 1)
	label := func(t float64) string {
		switch {
		case t < 4:
			return "Speaker 2"
		case t < 6:
			return "(unclear)" // detection had no answer for "Amen. It's"
		default:
			return "Speaker 1"
		}
	}
	got := Lines(ws, label, "(unclear)")
	want := "[0:00] Speaker 2: Have a blessed Sunday.\n[0:04] Speaker 1: Amen. It's good to be here, is it not?\n"
	if got != want {
		t.Errorf("Lines =\n%q\nwant\n%q", got, want)
	}
}

func TestRepeatsFindsLoops(t *testing.T) {
	loop := strings.Repeat("We ask that you open your word. ", 4)
	ws := words("He prayed. "+loop+"Amen. Amen. Amen. Then he taught.", 0, 1)
	got := Repeats(ws)
	if len(got) != 1 || got[0].Start != 2 {
		t.Errorf("Repeats = %+v; want one loop starting at 2 (short repeats like \"Amen.\" are not loops)", got)
	}
}
