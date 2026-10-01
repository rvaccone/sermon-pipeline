// Package describe writes the publishing text: YouTube title options, the YouTube description
// with chapters, and the podcast description. Claude writes the prose; this package validates it
// against YouTube's rules and assembles the final text deterministically.
package describe

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// Text is the finished publishing text.
type Text struct {
	body       string    // Claude's prose, before scripture, chapters and footer are added
	Titles     []string  `json:"titles"`
	YouTube    string    `json:"youtube_description"`
	Podcast    string    `json:"podcast_description"`
	Chapters   []Chapter `json:"chapters"`
	Passage    string    `json:"passage"`
	Scripture  []string  `json:"scripture"`
	Tags       []string  `json:"tags"`                  // YouTube tags, within YouTube's 500-character limit
	Hashtags   []string  `json:"hashtags"`              // shown at the end of the description
	Adjustment []string  `json:"adjustments,omitempty"` // fixes applied to Claude's answer
}

// Chapter is a YouTube chapter, timed from the start of the sermon video.
type Chapter struct {
	At    float64 `json:"at"`
	Title string  `json:"title"`
}

// Input is what the description is written from.
type Input struct {
	Words    []transcript.Word // the sermon's words, on the source timeline
	Sermon   timeline.Span
	Church   string
	Mission  string // the church's mission statement, for its voice
	Location string // e.g. "Clearwater, FL", which helps local search
	Website  string
	Preacher string // optional
	Date     string // YYYY-MM-DD
}

type answer struct {
	Titles   []string `json:"titles" jsonschema_description:"Three title options, keywords first, under 70 characters"`
	YouTube  string   `json:"youtube_description" jsonschema_description:"The description's prose: a keyword-rich opening sentence, then two to four short paragraphs"`
	Podcast  string   `json:"podcast_description" jsonschema_description:"One paragraph, at most 600 characters"`
	Chapters []struct {
		Time  string `json:"time" jsonschema_description:"Time from the start of the sermon, e.g. 12:34"`
		Title string `json:"title"`
	} `json:"chapters"`
	Passage   string   `json:"passage" jsonschema_description:"The main passage, e.g. Matthew 7:1-6"`
	Scripture []string `json:"scripture"`
	Tags      []string `json:"tags" jsonschema_description:"10-15 YouTube search tags, specific to this sermon first, then general"`
	Hashtags  []string `json:"hashtags" jsonschema_description:"Two or three hashtags, e.g. #Sermon"`
}

// YouTube's limits for descriptions, chapters and tags, and the podcast description's length.
const (
	maxDescriptionBytes = 5000
	minChapters         = 3
	minChapterSeconds   = 10
	maxTagChars         = 500
	maxHashtags         = 3
	maxPodcastChars     = 600
)

// Write asks Claude for the text and assembles the final descriptions. If the answer breaks one of
// YouTube's rules, Claude is asked once more with the problem stated; if it still does, the text
// is published without chapters and the adjustment is reported rather than failing the run.
func Write(ctx context.Context, ask llm.Asker, in Input) (Text, error) {
	relative := make([]transcript.Word, len(in.Words))
	for i, w := range in.Words {
		relative[i] = transcript.Word{Text: w.Text, Start: w.Start - in.Sermon.Start, End: w.End - in.Sermon.Start}
	}
	header := fmt.Sprintf("Church: %s\nLocation: %s\nMission: %s\nDate: %s\n", in.Church, in.Location, in.Mission, in.Date)
	if in.Preacher != "" {
		header += "Preacher: " + in.Preacher + "\n"
	}
	request := header + "\nTranscript:\n" + transcript.Lines(relative, nil, "")

	var text Text
	var problem error
	for attempt := 0; attempt < 2; attempt++ {
		if problem != nil {
			request += fmt.Sprintf("\n\nYour previous answer could not be used: %v. Please fix that.", problem)
		}
		var a answer
		if err := ask.Ask(ctx, llm.Load("descriptions"), request, &a); err != nil {
			return Text{}, err
		}
		if text, problem = build(a, in); problem == nil {
			return text, nil
		}
	}
	text.Adjustment = append(text.Adjustment, fmt.Sprintf("%v; published without chapters", problem))
	text.Chapters = nil
	text.YouTube = trimToLimit(assemble(text.body, text, in), maxDescriptionBytes)
	return text, nil
}

// build validates Claude's answer and assembles the text, reporting the first rule it breaks.
func build(a answer, in Input) (Text, error) {
	text := Text{Podcast: limitChars(clean(a.Podcast), maxPodcastChars), Passage: clean(a.Passage), body: clean(a.YouTube)}
	for _, t := range a.Titles {
		text.Titles = append(text.Titles, clean(t))
	}
	for _, sc := range a.Scripture {
		text.Scripture = append(text.Scripture, clean(sc))
	}
	text.Tags = validTags(a.Tags)
	text.Hashtags = validHashtags(a.Hashtags)
	for _, c := range a.Chapters {
		at, err := timeline.Parse(c.Time)
		if err != nil {
			text.Adjustment = append(text.Adjustment, fmt.Sprintf("dropped chapter %q with unreadable time %q", c.Title, c.Time))
			continue
		}
		text.Chapters = append(text.Chapters, Chapter{At: at, Title: clean(c.Title)})
	}
	var fixes []string
	text.Chapters, fixes = validChapters(text.Chapters, in.Sermon.Duration())
	text.Adjustment = append(text.Adjustment, fixes...)
	text.YouTube = assemble(text.body, text, in)
	switch {
	case len(text.Chapters) < minChapters:
		return text, fmt.Errorf("only %d usable chapters (YouTube needs at least %d, each at least %d seconds apart)",
			len(text.Chapters), minChapters, minChapterSeconds)
	case len(text.YouTube) > maxDescriptionBytes:
		return text, fmt.Errorf("the YouTube description is %d bytes (the limit is %d)", len(text.YouTube), maxDescriptionBytes)
	}
	return text, nil
}

// validChapters enforces YouTube's chapter rules: the first at 0:00, ascending, inside the video,
// and at least 10 seconds apart. It repairs what it can and reports each repair.
func validChapters(chapters []Chapter, duration float64) ([]Chapter, []string) {
	sort.SliceStable(chapters, func(i, j int) bool { return chapters[i].At < chapters[j].At })
	var out []Chapter
	var fixes []string
	for _, c := range chapters {
		switch {
		case c.At >= duration-minChapterSeconds:
			fixes = append(fixes, fmt.Sprintf("dropped chapter %q: too close to the end", c.Title))
		case len(out) > 0 && c.At-out[len(out)-1].At < minChapterSeconds:
			fixes = append(fixes, fmt.Sprintf("dropped chapter %q: less than 10 s after the previous one", c.Title))
		default:
			out = append(out, c)
		}
	}
	if len(out) > 0 && out[0].At != 0 {
		fixes = append(fixes, fmt.Sprintf("moved first chapter %q from %s to 0:00", out[0].Title, timeline.Clock(out[0].At)))
		out[0].At = 0
	}
	return out, fixes
}

// assemble builds the final YouTube description: prose, scripture, chapters, church footer.
func assemble(body string, t Text, in Input) string {
	var b strings.Builder
	b.WriteString(body)
	if len(t.Scripture) > 0 {
		b.WriteString("\n\nScripture: " + strings.Join(t.Scripture, " · "))
	}
	if len(t.Chapters) > 0 {
		b.WriteString("\n\n" + ChapterList(t.Chapters))
	}
	footer := in.Church
	if in.Location != "" {
		footer += " · " + in.Location
	}
	fmt.Fprintf(&b, "\n%s\n%s\n", footer, in.Website)
	if len(t.Hashtags) > 0 {
		b.WriteString("\n" + strings.Join(t.Hashtags, " ") + "\n")
	}
	return b.String()
}

// ChapterList writes chapters the way YouTube and Spotify read them from a description:
// "Chapters", then one "12:34 Title" line each.
func ChapterList(chapters []Chapter) string {
	var b strings.Builder
	b.WriteString("Chapters\n")
	for _, c := range chapters {
		fmt.Fprintf(&b, "%s %s\n", timeline.Clock(c.At), c.Title)
	}
	return b.String()
}

// validTags cleans and de-duplicates tags and keeps as many as fit YouTube's 500-character limit
// (tags are counted with the commas between them).
func validTags(tags []string) []string {
	var out []string
	seen := map[string]bool{}
	total := 0
	for _, t := range tags {
		t = strings.TrimSpace(strings.TrimPrefix(clean(t), "#"))
		key := strings.ToLower(t)
		if t == "" || seen[key] {
			continue
		}
		cost := len(t)
		if len(out) > 0 {
			cost++
		}
		if total+cost > maxTagChars {
			break
		}
		seen[key], total = true, total+cost
		out = append(out, t)
	}
	return out
}

var hashtagChars = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// validHashtags normalizes hashtags to "#Word" form (no spaces or punctuation) and keeps the first
// three; YouTube ignores all hashtags when a description has more than 15.
func validHashtags(tags []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range tags {
		word := hashtagChars.ReplaceAllString(t, "")
		if word == "" || seen[strings.ToLower(word)] {
			continue
		}
		seen[strings.ToLower(word)] = true
		out = append(out, "#"+word)
		if len(out) == maxHashtags {
			break
		}
	}
	return out
}

// trimToLimit drops whole paragraphs from the end of the prose until the text fits.
func trimToLimit(text string, limit int) string {
	for len(text) > limit {
		i := strings.LastIndex(strings.TrimRight(text[:limit], "\n"), "\n\n")
		if i <= 0 {
			return text[:limit]
		}
		text = text[:i] + "\n"
	}
	return text
}

// limitChars shortens s to at most n characters, ending at a sentence or word boundary.
func limitChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndexAny(cut, ".!?"); i > n/2 {
		return cut[:i+1]
	}
	return strings.TrimSpace(cut[:strings.LastIndex(cut, " ")]) + "…"
}

// clean removes characters YouTube rejects in titles and descriptions.
func clean(s string) string {
	return strings.TrimSpace(strings.NewReplacer("<", "", ">", "").Replace(s))
}
