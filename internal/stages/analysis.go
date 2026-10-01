package stages

import (
	"context"

	"github.com/rvaccone/sermon-pipeline/internal/cut"
	"github.com/rvaccone/sermon-pipeline/internal/diarize"
	"github.com/rvaccone/sermon-pipeline/internal/media"
	"github.com/rvaccone/sermon-pipeline/internal/sermon"
	"github.com/rvaccone/sermon-pipeline/internal/terms"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

func (j *Job) probe(ctx context.Context) error {
	info, err := media.Probe(ctx, j.Source)
	if err != nil {
		return err
	}
	return writeJSON(j.work("source.json"), info)
}

func (j *Job) analysisAudio(ctx context.Context) error {
	return media.ExtractAnalysisAudio(ctx, j.Source, j.work("audio16k.wav"))
}

func (j *Job) transcribe(ctx context.Context) error {
	t, err := transcript.Transcribe(ctx, transcript.WhisperOptions{
		Audio:    j.work("audio16k.wav"),
		Model:    j.Models.Whisper,
		VAD:      j.Models.VAD,
		Language: j.Config.Transcription.Language,
		WorkDir:  j.work(""),
	})
	if err != nil {
		return err
	}
	return writeJSON(j.work("transcript.json"), t)
}

func (j *Job) diarize(ctx context.Context) error {
	turns, err := diarize.Run(ctx, diarize.Options{
		Audio:        j.work("audio16k.wav"),
		Segmentation: j.Models.Segmentation,
		Embedding:    j.Models.Embedding,
	})
	if err != nil {
		return err
	}
	return writeJSON(j.work("speakers.json"), turns)
}

func (j *Job) findSermon(ctx context.Context) error {
	var (
		info  media.Info
		t     transcript.Transcript
		turns []diarize.Turn
	)
	if err := readAll(
		load{j.work("source.json"), &info},
		load{j.work("transcript.json"), &t},
		load{j.work("speakers.json"), &turns},
	); err != nil {
		return err
	}
	var b sermon.Boundary
	if j.Override != nil {
		b = sermon.FromOverride(*j.Override, turns)
	} else {
		claude, err := j.Claude()
		if err != nil {
			return err
		}
		if b, err = sermon.Detect(ctx, claude, sermon.Input{
			Transcript: t, Turns: turns, Ends: j.Config.Sermon.Ends,
			Duration: info.Duration, Pauses: j.pauses(),
		}); err != nil {
			return err
		}
	}
	return writeJSON(j.work("sermon.json"), b)
}

func (j *Job) correct(ctx context.Context) error {
	var (
		t transcript.Transcript
		b sermon.Boundary
	)
	if err := readAll(load{j.work("transcript.json"), &t}, load{j.work("sermon.json"), &b}); err != nil {
		return err
	}
	claude, err := j.Claude()
	if err != nil {
		return err
	}
	fixed, outcomes, err := terms.Correct(ctx, claude, t, b.Span, j.Config.Transcription.Glossary)
	if err != nil {
		return err
	}
	if err := writeJSON(j.work("corrections.json"), outcomes); err != nil {
		return err
	}
	return writeJSON(j.work("transcript-corrected.json"), fixed)
}

// pauses finds silences in the analysis audio. The thresholds come from the probe: -35 dB for
// at least 0.2 s separates words cleanly in a sanctuary recording.
func (j *Job) pauses() cut.Pauses {
	return func(ctx context.Context, within timeline.Span) ([]timeline.Span, error) {
		return media.Silences(ctx, j.work("audio16k.wav"), within, -35, 0.2)
	}
}

// load pairs a JSON file with where to decode it.
type load struct {
	path string
	into any
}

func readAll(loads ...load) error {
	for _, l := range loads {
		if err := readJSON(l.path, l.into); err != nil {
			return err
		}
	}
	return nil
}
