package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

type fakeAsker struct{ reply string }

func (f fakeAsker) Ask(_ context.Context, _ llm.Prompt, _ string, out any) error {
	return json.Unmarshal([]byte(f.reply), out)
}

func TestValidChapters(t *testing.T) {
	in := []Chapter{{90, "Two"}, {5, "One"}, {95, "Too close"}, {600, "Three"}, {1195, "At the end"}}
	got, fixes := validChapters(in, 1200)
	want := []Chapter{{0, "One"}, {90, "Two"}, {600, "Three"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("chapter %d = %+v; want %+v", i, got[i], want[i])
		}
	}
	if len(fixes) != 3 {
		t.Errorf("fixes = %q; want three reported repairs", fixes)
	}
}

func TestWriteAssemblesDescription(t *testing.T) {
	reply := `{"titles":["A | Matthew 7:1-6"],"youtube_description":"Body with <brackets>.","podcast_description":"Pod.",
		"chapters":[{"time":"0:00","title":"Opening"},{"time":"5:00","title":"Middle"},{"time":"20:00","title":"Close"}],
		"passage":"Matthew 7:1-6","scripture":["Matthew 7:1-6","Luke 15:11-32"],
		"tags":["Matthew 7 sermon","judging others","Matthew 7 Sermon"],"hashtags":["#Sermon","Matthew 7","#Bible Study","#Church","#Extra"]}`
	in := Input{
		Words:  []transcript.Word{{Text: "Hello.", Start: 100, End: 101}},
		Sermon: timeline.Span{Start: 100, End: 100 + 1800},
		Church: "Feather Sound Church", Location: "Clearwater, FL", Website: "feathersoundchurch.com", Date: "2026-07-26",
	}
	got, err := Write(context.Background(), fakeAsker{reply}, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Body with brackets.", "Scripture: Matthew 7:1-6 · Luke 15:11-32", "Chapters\n0:00 Opening\n5:00 Middle\n20:00 Close", "Feather Sound Church · Clearwater, FL\nfeathersoundchurch.com", "#Sermon #Matthew7 #BibleStudy\n"} {
		if !strings.Contains(got.YouTube, want) {
			t.Errorf("description missing %q:\n%s", want, got.YouTube)
		}
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags = %q; duplicates (case-insensitive) must be dropped", got.Tags)
	}
}

func TestValidTagsRespectsYouTubeLimit(t *testing.T) {
	var tags []string
	for i := 0; i < 60; i++ {
		tags = append(tags, fmt.Sprintf("sermon tag number %02d", i))
	}
	total := len(strings.Join(validTags(tags), ","))
	if total > 500 || total < 450 {
		t.Errorf("tags use %d characters; want as many as fit in 500", total)
	}
}

// sequenceAsker answers each request with the next reply.
type sequenceAsker struct{ replies []string }

func (s *sequenceAsker) Ask(_ context.Context, _ llm.Prompt, _ string, out any) error {
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return json.Unmarshal([]byte(reply), out)
}

func TestWriteRetriesThenPublishesWithoutChapters(t *testing.T) {
	tooFew := `{"titles":["T"],"youtube_description":"Body.","podcast_description":"Pod.",
		"chapters":[{"time":"0:00","title":"Only one"}],"passage":"Matthew 7:1-6","scripture":[],"tags":[],"hashtags":[]}`
	ask := &sequenceAsker{replies: []string{tooFew, tooFew}}
	in := Input{Sermon: timeline.Span{Start: 0, End: 1800}, Church: "C", Website: "w"}
	got, err := Write(context.Background(), ask, in)
	if err != nil {
		t.Fatalf("Write should degrade, not fail: %v", err)
	}
	if len(ask.replies) != 0 {
		t.Error("Write should ask a second time before giving up on chapters")
	}
	if strings.Contains(got.YouTube, "Chapters") || len(got.Adjustment) == 0 {
		t.Errorf("want no chapters and a reported adjustment, got %q / %q", got.YouTube, got.Adjustment)
	}
}

func TestLimitChars(t *testing.T) {
	long := strings.Repeat("Grace abounds here. ", 40)
	if got := limitChars(long, 600); len([]rune(got)) > 600 || !strings.HasSuffix(got, ".") {
		t.Errorf("limitChars = %q", got)
	}
}
