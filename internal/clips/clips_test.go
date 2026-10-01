package clips

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/diarize"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

func TestCameraKeepsHimCentered(t *testing.T) {
	// He walks steadily from 30% to 70% of the frame over 10 seconds.
	var frames []vision.PoseFrame
	for i := 0; i <= 300; i++ {
		tm := float64(i) / 30
		frames = append(frames, vision.PoseFrame{T: 100 + tm, Necks: []vision.Point{{0.3 + 0.04*tm, 0.3}}})
	}
	cam := Camera{FrameW: 1280, WindowW: 576, Smoothing: 0.3}
	left, ok := cam.Path(frames, 100, 300)
	if !ok {
		t.Fatal("tracking should be trusted with full coverage")
	}
	for i := 30; i < 270; i += 30 {
		neck := (0.3 + 0.04*float64(i)/30) * 1280
		if off := math.Abs(neck - (left[i] + 288)); off > 3 {
			t.Errorf("frame %d: %.1f px off-center", i, off)
		}
	}
}

func TestCameraClampsToFrameAndFallsBack(t *testing.T) {
	cam := Camera{FrameW: 1280, WindowW: 576, Smoothing: 0.3}
	edge := []vision.PoseFrame{{T: 0, Necks: []vision.Point{{0.99, 0.3}}}, {T: 1, Necks: []vision.Point{{0.99, 0.3}}}}
	left, _ := cam.Path(edge, 0, 30)
	if left[10] != 1280-576 {
		t.Errorf("left = %v; want clamped to %v", left[10], 1280-576)
	}
	empty := make([]vision.PoseFrame, 10)
	if _, ok := cam.Path(empty, 0, 30); ok {
		t.Error("no detections should fall back to the uncropped layout")
	}
}

type scriptedAsker struct {
	replies []string
	inputs  []string
}

func (s *scriptedAsker) Ask(_ context.Context, _ llm.Prompt, input string, out any) error {
	s.inputs = append(s.inputs, input)
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return json.Unmarshal([]byte(reply), out)
}

func TestPickChecksReviewsAndSnaps(t *testing.T) {
	text := strings.Repeat("filler words go here. ", 20) +
		"The critical mindset sees in others the sin it struggles with most. That is the log. " +
		strings.Repeat("more teaching here now. ", 20)
	var words []transcript.Word
	for i, w := range strings.Fields(text) {
		tm := float64(i) * 1.0
		words = append(words, transcript.Word{Text: w, Start: tm, End: tm + 0.8})
	}
	in := PickInput{
		Transcript: transcript.Transcript{Words: words},
		Sermon:     timeline.Span{Start: 0, End: 200},
		Turns:      []diarize.Turn{{Span: timeline.Span{Start: 0, End: 200}, Speaker: "p"}},
		Preacher:   "p", Count: 1, MinSeconds: 5, MaxSeconds: 60,
		Pauses: func(context.Context, timeline.Span) ([]timeline.Span, error) { return nil, nil },
	}
	ask := &scriptedAsker{replies: []string{
		`{"candidates":[
			{"start_quote":"The critical mindset sees","start_time":"1:20","end_quote":"That is the log.","end_time":"1:32","title":"The Sin We See","caption":"c"},
			{"start_quote":"never said","start_time":"0:10","end_quote":"anywhere","end_time":"0:20","title":"Invented","caption":"c"}]}`,
		`{"reviews":[{"number":1,"verdict":"keep","reason":"stands alone","score":8}]}`,
	}}
	sel, err := Pick(context.Background(), ask, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Clips) != 1 || sel.Clips[0].ID != "01-the-sin-we-see" || sel.Clips[0].Score != 8 {
		t.Fatalf("clips = %+v", sel.Clips)
	}
	if len(sel.Rejected) != 1 || sel.Rejected[0].Title != "Invented" {
		t.Errorf("rejected = %+v", sel.Rejected)
	}
	review := ask.inputs[1]
	for _, want := range []string{"Preacher: The critical mindset", "Caption: c", "Just before the clip:"} {
		if !strings.Contains(review, want) {
			t.Errorf("the reviewer's input is missing %q:\n%s", want, review)
		}
	}
	// Continuous speech with no pauses: the start may not cut into the previous word (which ends
	// at 79.8), and must still come before the first word at 80.
	if s := sel.Clips[0].Span; s.Start < 79.8 || s.Start > 80 {
		t.Errorf("span = %+v; the start must fall between the previous word and the first", s)
	}
}

func TestCaptionFileHighlightsEachWord(t *testing.T) {
	ws := []transcript.Word{{Text: "Come", Start: 10, End: 10.4}, {Text: "home.", Start: 10.5, End: 11}}
	ass := captionFile(ws, 10, "Arial Black")
	for _, want := range []string{
		`Dialogue: 0,0:00:00.00,0:00:00.50,Caption,,0,0,0,,{\c&H00D7FF&}COME{\c&HFFFFFF&} HOME.`,
		`Dialogue: 0,0:00:00.50,0:00:01.00,Caption,,0,0,0,,COME {\c&H00D7FF&}HOME.{\c&HFFFFFF&}`,
	} {
		if !strings.Contains(ass, want) {
			t.Errorf("missing %q in\n%s", want, ass)
		}
	}
}

func TestUnreviewedCandidatesAreRejected(t *testing.T) {
	var words []transcript.Word
	for i, w := range strings.Fields(strings.Repeat("Grace is enough for today. ", 40)) {
		tm := float64(i)
		words = append(words, transcript.Word{Text: w, Start: tm, End: tm + 0.8})
	}
	in := PickInput{
		Transcript: transcript.Transcript{Words: words}, Sermon: timeline.Span{Start: 0, End: 300},
		Preacher: "p", Count: 1, MinSeconds: 5, MaxSeconds: 60,
		Pauses: func(context.Context, timeline.Span) ([]timeline.Span, error) { return nil, nil },
	}
	ask := &scriptedAsker{replies: []string{
		`{"candidates":[{"start_quote":"Grace is enough","start_time":"0:20","end_quote":"for today.","end_time":"0:33","title":"A","caption":"c"}]}`,
		`{"reviews":[]}`,
	}}
	sel, err := Pick(context.Background(), ask, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Clips) != 0 || len(sel.Rejected) != 1 || !strings.Contains(sel.Rejected[0].Reason, "did not assess") {
		t.Errorf("selection = %+v; an unreviewed candidate must be recorded as rejected", sel)
	}
}
