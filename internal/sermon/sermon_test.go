package sermon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/diarize"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// fakeAsker answers every request with the same JSON.
type fakeAsker struct{ reply string }

func (f fakeAsker) Ask(_ context.Context, _ llm.Prompt, _ string, out any) error {
	return json.Unmarshal([]byte(f.reply), out)
}

func noPauses(context.Context, timeline.Span) ([]timeline.Span, error) { return nil, nil }

func service() transcript.Transcript {
	text := "That's all the announcements. Have a blessed Sunday. Amen it's good to be in the house of the Lord, Brother Doug here. " +
		strings.Repeat("Teaching words here. ", 300) + "And all God's people said amen. Have a great day."
	var words []transcript.Word
	for i, w := range strings.Fields(text) {
		t := float64(i) * 2
		words = append(words, transcript.Word{Text: w, Start: t, End: t + 1.5})
	}
	return transcript.Transcript{Words: words}
}

func TestDetect(t *testing.T) {
	tr := service()
	last := tr.Words[len(tr.Words)-1]
	turns := []diarize.Turn{
		{Span: timeline.Span{Start: 0, End: 15}, Speaker: "speaker_01"},
		{Span: timeline.Span{Start: 15, End: last.End + 1}, Speaker: "speaker_00"},
	}
	reply := `{"start_quote":"Amen it's good to be in the house","start_time":"0:16",
		"end_quote":"said amen. Have a great day.","end_time":"` + timeline.Clock(last.Start-8) + `",
		"preacher_name":"","confidence":"high","reasoning":"r","notes":[]}`
	b, err := Detect(context.Background(), fakeAsker{reply}, Input{
		Transcript: tr, Turns: turns, Ends: "after-closing-prayer", Duration: last.End + 5, Pauses: noPauses,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Span.Start != 16-0.3 || b.Span.End != last.End+0.5 {
		t.Errorf("span = %+v; want 15.7 .. %v", b.Span, last.End+0.5)
	}
	if len(b.Flags) != 1 || !strings.Contains(b.Flags[0], "no pause nearby") {
		t.Errorf("flags = %q; want only the no-pause flag", b.Flags)
	}
	if b.Preacher != "speaker_00" {
		t.Errorf("preacher = %q", b.Preacher)
	}
}

func TestDetectRejectsInventedQuotes(t *testing.T) {
	reply := `{"start_quote":"words that were never said","start_time":"0:10","end_quote":"x","end_time":"0:20",
		"preacher_name":"","confidence":"high","reasoning":"","notes":[]}`
	_, err := Detect(context.Background(), fakeAsker{reply}, Input{Transcript: service(), Duration: 2000, Pauses: noPauses})
	if err == nil || !strings.Contains(err.Error(), "not in the transcript") {
		t.Errorf("err = %v; want a not-found error", err)
	}
}

func TestOverrideFlagsOtherSpeakers(t *testing.T) {
	turns := []diarize.Turn{
		{Span: timeline.Span{Start: 0, End: 1000}, Speaker: "a"},
		{Span: timeline.Span{Start: 1000, End: 1200}, Speaker: "b"},
		{Span: timeline.Span{Start: 1200, End: 3000}, Speaker: "a"},
	}
	b := FromOverride(timeline.Span{Start: 10, End: 2990}, turns)
	if b.Preacher != "a" || len(b.Flags) != 1 || !strings.Contains(b.Flags[0], "other than the preacher speaks") {
		t.Errorf("boundary = %+v", b)
	}
}

func TestDetectDropsAnInventedPreacherName(t *testing.T) {
	tr := service()
	last := tr.Words[len(tr.Words)-1]
	reply := `{"start_quote":"Amen it's good to be in the house","start_time":"0:16",
		"end_quote":"said amen. Have a great day.","end_time":"` + timeline.Clock(last.Start-8) + `",
		"preacher_name":"Brother Invented","confidence":"high","reasoning":"r","notes":[]}`
	b, err := Detect(context.Background(), fakeAsker{reply}, Input{Transcript: tr, Duration: last.End + 5, Pauses: noPauses})
	if err != nil {
		t.Fatal(err)
	}
	if b.PreacherName != "" {
		t.Errorf("PreacherName = %q; a name not in the transcript must be dropped even though \"Brother\" was said", b.PreacherName)
	}
}

func TestDetectNamesAListedPreacher(t *testing.T) {
	tr := service() // the preacher says "Brother Doug here."
	last := tr.Words[len(tr.Words)-1]
	for _, tc := range []struct{ claude, want string }{
		{"Doug", "Brother Doug Example"},                 // short form → the name as the church writes it
		{"Brother Doug Example", "Brother Doug Example"}, // surname not said, but "Doug" was
		{"Pastor Art Dykstra", ""},                       // listed, but nothing of the name was said
	} {
		reply := `{"start_quote":"Amen it's good to be in the house","start_time":"0:16",
			"end_quote":"said amen. Have a great day.","end_time":"` + timeline.Clock(last.Start-8) + `",
			"preacher_name":"` + tc.claude + `","confidence":"high","reasoning":"r","notes":[]}`
		b, err := Detect(context.Background(), fakeAsker{reply}, Input{
			Transcript: tr, Duration: last.End + 5, Pauses: noPauses,
			Preachers: []string{"Pastor Art Dykstra", "Brother Doug Example"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if b.PreacherName != tc.want {
			t.Errorf("Claude said %q: PreacherName = %q, want %q", tc.claude, b.PreacherName, tc.want)
		}
	}
}
