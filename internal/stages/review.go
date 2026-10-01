package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/clips"
	"github.com/rvaccone/sermon-pipeline/internal/describe"
	"github.com/rvaccone/sermon-pipeline/internal/media"
	"github.com/rvaccone/sermon-pipeline/internal/review"
	"github.com/rvaccone/sermon-pipeline/internal/sermon"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// review builds review.html from whatever the other stages produced. It runs even when some of
// them failed, and says which outputs are missing instead of hiding the ones that worked.
func (j *Job) review(ctx context.Context) error {
	var b sermon.Boundary
	if err := readJSON(j.work("sermon.json"), &b); err != nil {
		return err
	}
	var t transcript.Transcript
	if err := readJSON(j.work("transcript.json"), &t); err != nil {
		return err
	}
	var (
		text                   describe.Text
		videoLoud, podcastLoud loudness
		sel                    clips.Selection
		renders                clipRenders
		thumbSet               thumbnailSet
		fixes                  []transcript.Outcome
		missing                []string
	)
	optional := func(path, what string, into any) bool {
		if err := readJSON(path, into); err != nil {
			missing = append(missing, what)
			return false
		}
		return true
	}
	optional(j.work("descriptions.json"), "titles and descriptions", &text)
	optional(j.work("loudness-video.json"), "the video's mastered audio", &videoLoud)
	optional(j.work("loudness-podcast.json"), "the podcast's mastered audio", &podcastLoud)
	optional(j.work("corrections.json"), "transcript corrections", &fixes)
	optional(j.work("thumbnails.json"), "thumbnails", &thumbSet)
	if optional(j.work("clips.json"), "clip picks", &sel) {
		optional(j.work("clip-renders.json"), "rendered clips", &renders)
	}
	for _, f := range []struct{ file, what string }{
		{youtubeVideo, "the sermon video"}, {podcastAudio, "the podcast audio"}, {youtubeCaptions, "captions"},
	} {
		if _, err := os.Stat(j.out(f.file)); err != nil {
			missing = append(missing, f.what)
		}
	}

	page := review.Page{
		Title:  firstOr(text.Titles, filepath.Base(j.Source)),
		Church: j.Config.Church.Name,
		Date:   j.Date,
		Flags:  append(repeatFlags(t, b), j.flags(b, text, sel, renders, thumbSet, missing)...),
		Sermon: review.Sermon{
			Start: timeline.Precise(b.Span.Start), End: timeline.Precise(b.Span.End),
			Length: timeline.Clock(b.Span.Duration()), StartQuote: b.StartQuote, EndQuote: b.EndQuote,
			Source: b.Source, Confidence: b.Confidence, Reasoning: b.Reasoning,
		},
		Video:    review.Media{File: youtubeVideo, Loudness: describeLoudness(videoLoud)},
		Podcast:  review.Media{File: podcastAudio, Loudness: describeLoudness(podcastLoud)},
		Captions: youtubeCaptions,
		Text:     review.Text{Titles: text.Titles, YouTube: text.YouTube, Podcast: text.Podcast},
	}
	for _, c := range sel.Clips {
		page.Clips = append(page.Clips, review.Clip{
			File: clipsDir + "/" + c.ID + ".mp4", Title: c.Title, Caption: c.Caption,
			Length: timeline.Clock(c.Span.Duration()), Score: c.Score, Review: c.Review,
			Uncropped: renders.Uncropped[c.ID],
		})
	}
	for _, r := range sel.Rejected {
		page.Rejected = append(page.Rejected, fmt.Sprintf("%q (%s)", r.Title, r.Reason))
	}
	for _, f := range thumbSet.Files {
		page.Thumbs = append(page.Thumbs, youtubeThumbs+"/"+filepath.Base(f))
	}
	for _, f := range fixes {
		line := fmt.Sprintf("“%s” → “%s” at %s (%s)", f.Find, f.Replace, f.Time, f.Reason)
		if f.Applied {
			page.Fixes = append(page.Fixes, line)
		} else {
			page.Refused = append(page.Refused, line+": "+f.Skipped)
		}
	}
	if err := review.Write(j.out(reviewPage), page); err != nil {
		return err
	}
	return j.export(exportName(j.Date, text))
}

// flags gathers everything a person should check before publishing.
func (j *Job) flags(b sermon.Boundary, text describe.Text, sel clips.Selection, renders clipRenders, thumbSet thumbnailSet, missing []string) []string {
	var flags []string
	if len(missing) > 0 {
		flags = append(flags, "Not produced (a stage failed; see the terminal or run.json): "+strings.Join(missing, ", ")+".")
	}
	flags = append(flags, b.Flags...)
	for _, a := range text.Adjustment {
		flags = append(flags, "Description: "+a)
	}
	if want := j.Config.Clips.Count; sel.Clips != nil && len(sel.Clips) < want {
		flags = append(flags, fmt.Sprintf("Only %d of %d clips passed the checks and review.", len(sel.Clips), want))
	}
	var uncropped []string
	for id, u := range renders.Uncropped {
		if u {
			uncropped = append(uncropped, id)
		}
	}
	if len(uncropped) > 0 {
		sort.Strings(uncropped)
		flags = append(flags, "Tracking was unreliable, so these clips show the full frame: "+strings.Join(uncropped, ", "))
	}
	if thumbSet.Chosen != nil && len(thumbSet.Files) == 0 {
		flags = append(flags, "No usable face frames were found for thumbnails.")
	}
	return flags
}

// repeatFlags reports any repetition loop the transcription couldn't break inside the sermon.
func repeatFlags(t transcript.Transcript, b sermon.Boundary) []string {
	var flags []string
	for _, r := range t.Repeats {
		if r.Overlap(b.Span) > 0 {
			flags = append(flags, fmt.Sprintf("The transcript repeats itself at %s, so captions and text there may be wrong.", r))
		}
	}
	return flags
}

func describeLoudness(l media.Loudness) string {
	if l == (media.Loudness{}) {
		return ""
	}
	return fmt.Sprintf("%.1f LUFS · peak %.1f dBTP · range %.1f LU", l.Integrated, l.TruePeak, l.Range)
}

func firstOr(list []string, fallback string) string {
	if len(list) > 0 {
		return list[0]
	}
	return fallback
}
