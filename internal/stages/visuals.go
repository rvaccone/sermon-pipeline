package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/clips"
	"github.com/rvaccone/sermon-pipeline/internal/describe"
	"github.com/rvaccone/sermon-pipeline/internal/diarize"
	"github.com/rvaccone/sermon-pipeline/internal/media"
	"github.com/rvaccone/sermon-pipeline/internal/sermon"
	"github.com/rvaccone/sermon-pipeline/internal/thumbs"
	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

// clipRenders is work/clip-renders.json: which clips fell back to the uncropped layout.
type clipRenders struct {
	Uncropped map[string]bool `json:"uncropped"`
}

func (j *Job) pickClips(ctx context.Context) error {
	t, b, err := j.correctedSermon()
	if err != nil {
		return err
	}
	var turns []diarize.Turn
	if err := readJSON(j.work("speakers.json"), &turns); err != nil {
		return err
	}
	claude, err := j.Claude()
	if err != nil {
		return err
	}
	c := j.Config.Clips
	sel, err := clips.Pick(ctx, claude, clips.PickInput{
		Transcript: t, Sermon: b.Span, Turns: turns, Preacher: b.Preacher,
		Count: c.Count, MinScore: c.MinScore, MinSeconds: c.MinSeconds, MaxSeconds: c.MaxSeconds, Pauses: j.pauses(),
	})
	if err != nil {
		return err
	}
	var post strings.Builder
	for _, clip := range sel.Clips {
		fmt.Fprintf(&post, "%s.mp4 (%.0f s)\n  Title:   %s\n  Caption: %s\n\n", clip.ID, clip.Span.Duration(), clip.Title, clip.Caption)
	}
	if err := os.WriteFile(j.out(clipPostCaptions), []byte(post.String()), 0o644); err != nil {
		return err
	}
	return writeJSON(j.work("clips.json"), sel)
}

func (j *Job) renderClips(ctx context.Context) error {
	var (
		info media.Info
		sel  clips.Selection
	)
	if err := readAll(load{j.work("source.json"), &info}, load{j.work("clips.json"), &sel}); err != nil {
		return err
	}
	t, _, err := j.correctedSermon()
	if err != nil {
		return err
	}
	if err := removeStale(j.out(clipsDir), ".mp4", j.clipFiles()); err != nil {
		return err
	}
	c := j.Config.Clips
	cam := clips.Camera{FrameW: float64(info.Width), WindowW: clips.WindowWidth(info.Height), Smoothing: c.SmoothingSeconds}
	renders := clipRenders{Uncropped: map[string]bool{}}
	for _, clip := range sel.Clips {
		frames, err := j.Vision.Pose(ctx, j.Source, clip.Span, 30)
		if err != nil {
			return fmt.Errorf("tracking %s: %w", clip.ID, err)
		}
		left, tracked := cam.Path(frames, clip.Span.Start, clips.FrameCount(clip.Span.Duration()))
		renders.Uncropped[clip.ID] = !tracked

		// Each clip's audio is mastered on its own, like the sermon's, so every clip lands at the
		// same loudness whatever part of the sermon it comes from.
		audio := j.work(clip.ID + ".wav")
		if _, err := media.Master(ctx, media.MasterSpec{
			Source: j.Source, Span: clip.Span, Channels: 2, Rate: 48000,
			LUFS: j.Config.Audio.VideoLUFS, TruePeak: j.Config.Audio.TruePeak, FadeIn: 0.15, FadeOut: 0.3,
			Dest: audio, Check: media.CheckAAC("192k"),
		}); err != nil {
			return fmt.Errorf("mastering audio for %s: %w", clip.ID, err)
		}
		if err := clips.Render(ctx, clips.RenderSpec{
			ID: clip.ID, Source: j.Source, Span: clip.Span, Audio: audio, FrameH: info.Height, Left: left,
			Words: t.Within(clip.Span), FontFamily: c.FontFamily, FontDir: filepath.Dir(c.FontFile),
			Preset: j.Config.Video.Preset, CRF: c.CRF, WorkDir: j.work(""), Dest: j.clipFile(clip.ID),
		}); err != nil {
			return fmt.Errorf("rendering %s: %w", clip.ID, err)
		}
	}
	return writeJSON(j.work("clip-renders.json"), renders)
}

func (j *Job) clipFile(id string) string { return j.out(clipsDir + "/" + id + ".mp4") }

// clipFiles lists the rendered clips the current selection calls for.
func (j *Job) clipFiles() []string {
	files := []string{j.work("clip-renders.json")}
	var sel clips.Selection
	if readJSON(j.work("clips.json"), &sel) == nil {
		for _, c := range sel.Clips {
			files = append(files, j.clipFile(c.ID))
		}
	}
	return files
}

func (j *Job) thumbnailFrames(ctx context.Context) error {
	var b sermon.Boundary
	if err := readJSON(j.work("sermon.json"), &b); err != nil {
		return err
	}
	frames, err := j.Vision.Faces(ctx, j.Source, b.Span, 0.5)
	if err != nil {
		return err
	}
	return writeJSON(j.work("faces.json"), frames)
}

// thumbnailSet is work/thumbnails.json: the frames chosen and the files made from them.
type thumbnailSet struct {
	Chosen []thumbs.Candidate `json:"chosen"`
	Files  []string           `json:"files"`
}

func (j *Job) thumbnails(ctx context.Context) error {
	var (
		info   media.Info
		frames []vision.FaceFrame
		text   describe.Text
	)
	if err := readAll(load{j.work("source.json"), &info}, load{j.work("faces.json"), &frames}, load{j.work("descriptions.json"), &text}); err != nil {
		return err
	}
	set := thumbnailSet{Chosen: thumbs.Choose(frames, j.Config.Thumbnails.Count)}
	for i := range set.Chosen {
		set.Files = append(set.Files, j.out(fmt.Sprintf("%s/%d.jpg", youtubeThumbs, i+1)))
	}
	if err := removeStale(j.out(youtubeThumbs), ".jpg", set.Files); err != nil {
		return err
	}
	title := text.Passage
	if len(text.Titles) > 0 {
		title, _, _ = strings.Cut(text.Titles[0], " | ")
	}
	for i, frame := range set.Chosen {
		if err := thumbs.Compose(ctx, thumbs.Spec{
			Source: j.Source, Width: info.Width, Height: info.Height, Frame: frame,
			Title: title, Passage: text.Passage, Font: j.Config.Thumbnails.FontFile, Dest: set.Files[i],
		}); err != nil {
			return fmt.Errorf("thumbnail %d: %w", i+1, err)
		}
	}
	return writeJSON(j.work("thumbnails.json"), set)
}

func (j *Job) thumbnailFiles() []string {
	files := []string{j.work("thumbnails.json")}
	var set thumbnailSet
	if readJSON(j.work("thumbnails.json"), &set) == nil {
		files = append(files, set.Files...)
	}
	return files
}

// removeStale deletes files with ext in dir that are no longer wanted, so a new selection never
// leaves an old clip or thumbnail behind.
func removeStale(dir, ext string, keep []string) error {
	wanted := map[string]bool{}
	for _, k := range keep {
		wanted[k] = true
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*"+ext))
	if err != nil {
		return err
	}
	for _, m := range matches {
		if !wanted[m] {
			if err := os.Remove(m); err != nil {
				return err
			}
		}
	}
	return nil
}
