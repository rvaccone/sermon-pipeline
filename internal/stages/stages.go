package stages

import (
	"os"

	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/pipeline"
)

// SermonStage is the stage that decides the sermon's span; `sermon eval` runs up to it.
const SermonStage = "sermon"

// Stages returns the full pipeline. Each stage's Inputs lists the settings that affect its
// result; changing one re-runs that stage and everything downstream of it.
func (j *Job) Stages() []pipeline.Stage {
	c := j.Config
	files := func(paths ...string) func() []string { return func() []string { return paths } }
	claude := func(prompt string) any {
		return []string{c.Claude.Model, c.Claude.Effort, llm.Load(prompt).Version}
	}
	return []pipeline.Stage{
		{
			Name: "probe", Version: 1,
			Inputs:  func() any { return []any{j.sourceIdentity(), j.uses(ffmpeg)} },
			Outputs: files(j.work("source.json")),
			Run:     j.probe,
		},
		{
			Name: "analysis-audio", Version: 1, Needs: []string{"probe"},
			Inputs:  func() any { return j.uses(ffmpeg) },
			Outputs: files(j.work("audio16k.wav")),
			Run:     j.analysisAudio,
		},
		{
			Name: "transcribe", Version: 4, Needs: []string{"analysis-audio"},
			Inputs: func() any {
				return []any{c.Transcription.Language, j.uses(ffmpeg, whisperCLI, whisperVAD, whisperFile, vadFile)}
			},
			Outputs: files(j.work("transcript.json")),
			Run:     j.transcribe,
		},
		{
			Name: "diarize", Version: 2, Needs: []string{"analysis-audio"},
			Inputs:  func() any { return j.uses(sherpa, segFile, embedFile) },
			Outputs: files(j.work("speakers.json")),
			Run:     j.diarize,
		},
		{
			Name: SermonStage, Version: 3, Needs: []string{"probe", "transcribe", "diarize"},
			Inputs:  func() any { return []any{c.Sermon, j.Override, claude("sermon-boundaries"), j.uses(ffmpeg)} },
			Outputs: files(j.work("sermon.json")),
			Run:     j.findSermon,
		},
		{
			Name: "corrections", Version: 2, Needs: []string{"transcribe", "sermon"},
			Inputs:  func() any { return []any{c.Transcription.Glossary, claude("transcript-corrections")} },
			Outputs: files(j.work("transcript-corrected.json"), j.work("corrections.json")),
			Run:     j.correct,
		},
		{
			Name: "captions", Version: 1, Needs: []string{"corrections", "sermon"},
			Outputs: files(j.out(youtubeCaptions), j.out(transcriptText)),
			Run:     j.captions,
		},
		{
			Name: "video-audio", Version: 1, Needs: []string{SermonStage},
			Inputs:  func() any { return []any{c.Audio.VideoLUFS, c.Audio.TruePeak, j.uses(ffmpeg)} },
			Outputs: files(j.work("master-video.wav"), j.work("loudness-video.json")),
			Run:     j.masterVideoAudio,
		},
		{
			Name: "podcast-audio", Version: 1, Needs: []string{SermonStage},
			Inputs: func() any {
				return []any{c.Audio.PodcastLUFS, c.Audio.TruePeak, c.Audio.PodcastBitrate, j.uses(ffmpeg)}
			},
			Outputs: files(j.work("master-podcast.wav"), j.work("loudness-podcast.json")),
			Run:     j.masterPodcastAudio,
		},
		{
			Name: "video", Version: 1, Needs: []string{"probe", SermonStage, "video-audio"},
			Inputs:  func() any { return []any{c.Video, j.uses(ffmpeg)} },
			Outputs: files(j.out(youtubeVideo)),
			Run:     j.video,
		},
		{
			Name: "podcast", Version: 1, Needs: []string{"podcast-audio"},
			Inputs:  func() any { return []any{c.Church, c.Audio.PodcastBitrate, j.Date, j.uses(ffmpeg)} },
			Outputs: files(j.out(podcastAudio)),
			Run:     j.podcast,
		},
		{
			Name: "describe", Version: 2, Needs: []string{"corrections", "sermon"},
			Inputs: func() any { return []any{c.Church, j.Preacher, j.Date, claude("descriptions")} },
			Outputs: files(j.work("descriptions.json"), j.out(youtubeDesc), j.out(youtubeTitles),
				j.out(youtubeTags), j.out(podcastDesc)),
			Run: j.describe,
		},
		{
			Name: "clip-picks", Version: 3, Needs: []string{"corrections", "diarize", "sermon"},
			Inputs: func() any {
				return []any{c.Clips.Count, c.Clips.MinScore, c.Clips.MinSeconds, c.Clips.MaxSeconds,
					claude("clip-candidates"), claude("clip-review"), j.uses(ffmpeg)}
			},
			Outputs: files(j.work("clips.json"), j.out(clipPostCaptions)),
			Run:     j.pickClips,
		},
		{
			Name: "clips", Version: 2, Needs: []string{"probe", "sermon", "corrections", "clip-picks"},
			Inputs: func() any {
				return []any{c.Clips.CRF, c.Clips.SmoothingSeconds, c.Clips.FontFamily, c.Clips.FontFile,
					c.Video.Preset, c.Audio, j.uses(ffmpeg, visionTool)}
			},
			Outputs: j.clipFiles,
			Run:     j.renderClips,
		},
		{
			Name: "thumbnail-frames", Version: 1, Needs: []string{SermonStage},
			Inputs:  func() any { return j.uses(visionTool) },
			Outputs: files(j.work("faces.json")),
			Run:     j.thumbnailFrames,
		},
		{
			Name: "thumbnails", Version: 2, Needs: []string{"probe", "thumbnail-frames", "describe"},
			Inputs:  func() any { return []any{c.Thumbnails, j.uses(ffmpeg, magick)} },
			Outputs: j.thumbnailFiles,
			Run:     j.thumbnails,
		},
		{
			// The review page runs last, even when some stages failed, and shows what failed.
			Name: "review", Version: 3, Needs: []string{SermonStage}, Finally: true,
			Outputs: j.exportFiles,
			Run:     j.review,
		},
	}
}

// sourceIdentity changes whenever the recording is replaced or edited.
func (j *Job) sourceIdentity() any {
	info, err := os.Stat(j.Source)
	if err != nil {
		return j.Source
	}
	return []any{j.Source, info.Size(), info.ModTime().UnixNano()}
}
