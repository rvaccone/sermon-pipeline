package stages

import (
	"context"
	"os"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/describe"
	"github.com/rvaccone/sermon-pipeline/internal/media"
	"github.com/rvaccone/sermon-pipeline/internal/sermon"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// loudness is work/loudness-video.json or work/loudness-podcast.json: what the delivered audio
// measured.
type loudness = media.Loudness

func (j *Job) captions(ctx context.Context) error {
	t, b, err := j.correctedSermon()
	if err != nil {
		return err
	}
	words := t.Within(b.Span)
	if err := os.WriteFile(j.out(youtubeCaptions),
		[]byte(transcript.SRT(transcript.Cues(words, transcript.YouTubeLimits), b.Span.Start)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(j.out(transcriptText), []byte(transcript.Paragraphs(words)), 0o644)
}

// masterVideoAudio masters the sermon's audio for YouTube: stereo, AAC, video loudness target.
func (j *Job) masterVideoAudio(ctx context.Context) error {
	return j.master(ctx, 2, 48000, j.Config.Audio.VideoLUFS, media.CheckAAC("320k"), "video")
}

// masterPodcastAudio masters it for the podcast: mono, MP3, podcast loudness target.
func (j *Job) masterPodcastAudio(ctx context.Context) error {
	a := j.Config.Audio
	return j.master(ctx, 1, 44100, a.PodcastLUFS, media.CheckMP3(a.PodcastBitrate), "podcast")
}

func (j *Job) master(ctx context.Context, channels, rate int, lufs float64, check func(context.Context, string) (media.Loudness, error), name string) error {
	var b sermon.Boundary
	if err := readJSON(j.work("sermon.json"), &b); err != nil {
		return err
	}
	result, err := media.Master(ctx, media.MasterSpec{
		Source: j.Source, Span: b.Span, Channels: channels, Rate: rate,
		LUFS: lufs, TruePeak: j.Config.Audio.TruePeak, FadeIn: media.FadeIn, FadeOut: media.FadeOut,
		Dest: j.work("master-" + name + ".wav"), Check: check,
	})
	if err != nil {
		return err
	}
	return writeJSON(j.work("loudness-"+name+".json"), result)
}

func (j *Job) video(ctx context.Context) error {
	var (
		info media.Info
		b    sermon.Boundary
	)
	if err := readAll(load{j.work("source.json"), &info}, load{j.work("sermon.json"), &b}); err != nil {
		return err
	}
	return media.RenderSermon(ctx, media.VideoSpec{
		Source: j.Source, Span: b.Span, FPS: info.FPS, Audio: j.work("master-video.wav"),
		Preset: j.Config.Video.Preset, CRF: j.Config.Video.CRF, Dest: j.out(youtubeVideo),
	})
}

func (j *Job) podcast(ctx context.Context) error {
	church := j.Config.Church
	return media.EncodePodcast(ctx, media.PodcastSpec{
		Audio: j.work("master-podcast.wav"), Bitrate: j.Config.Audio.PodcastBitrate,
		Title: "Sermon · " + j.Date, Artist: church.Name, Album: church.Podcast,
		Date: j.Date, Comment: church.Website, Dest: j.out(podcastAudio),
	})
}

func (j *Job) describe(ctx context.Context) error {
	t, b, err := j.correctedSermon()
	if err != nil {
		return err
	}
	claude, err := j.Claude()
	if err != nil {
		return err
	}
	preacher := j.Preacher
	if preacher == "" {
		preacher = b.PreacherName
	}
	text, err := describe.Write(ctx, claude, describe.Input{
		Words: t.Within(b.Span), Sermon: b.Span, Church: j.Config.Church.Name, Mission: j.Config.Church.Mission, Location: j.Config.Church.Location,
		Website: j.Config.Church.Website, Preacher: preacher, Date: j.Date,
	})
	if err != nil {
		return err
	}
	writes := map[string]string{
		j.out(youtubeDesc):   text.YouTube,
		j.out(youtubeTitles): strings.Join(text.Titles, "\n") + "\n",
		j.out(youtubeTags):   strings.Join(text.Tags, ", ") + "\n",
		j.out(podcastDesc):   text.Podcast + "\n",
	}
	for path, content := range writes {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return writeJSON(j.work("descriptions.json"), text)
}

// correctedSermon loads the corrected transcript and the sermon boundary.
func (j *Job) correctedSermon() (transcript.Transcript, sermon.Boundary, error) {
	var (
		t transcript.Transcript
		b sermon.Boundary
	)
	err := readAll(load{j.work("transcript-corrected.json"), &t}, load{j.work("sermon.json"), &b})
	return t, b, err
}
