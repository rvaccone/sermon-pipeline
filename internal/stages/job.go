// Package stages assembles the pipeline for one sermon: what each stage reads, does and writes.
// It is the only package that knows about every other one.
package stages

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/rvaccone/sermon-pipeline/internal/config"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
	"github.com/rvaccone/sermon-pipeline/internal/models"
	"github.com/rvaccone/sermon-pipeline/internal/pipeline"
	"github.com/rvaccone/sermon-pipeline/internal/slug"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

// Deliverables, relative to a sermon's output folder, grouped by where they get published.
const (
	reviewPage       = "Review.html"
	transcriptText   = "Transcript.txt"
	youtubeVideo     = "YouTube/Sermon.mp4"
	youtubeCaptions  = "YouTube/Captions.srt"
	youtubeTitles    = "YouTube/Title options.txt"
	youtubeDesc      = "YouTube/Description.txt"
	youtubeTags      = "YouTube/Tags.txt"
	youtubeThumbs    = "YouTube/Thumbnails"
	podcastAudio     = "Podcast/Episode.mp3"
	podcastDesc      = "Podcast/Description.txt"
	clipsDir         = "Clips"
	clipPostCaptions = "Clips/Post captions.txt"
)

// Job is one run over one recording.
type Job struct {
	Config   config.Config
	Source   string         // the service recording
	Date     string         // service date, YYYY-MM-DD
	Preacher string         // optional; otherwise taken from the transcript when stated
	Override *timeline.Span // sermon span chosen by a person, used instead of detection
	Root     string         // where each sermon's output folder is created, e.g. ~/Sermons
	Dir      string         // workspace: work/ holds intermediates, out/ the deliverables
	Models   models.Set
	Vision   vision.Helper

	claudeOnce sync.Once
	claude     llm.Asker
	claudeErr  error

	toolchainOnce sync.Once
	toolchain     map[string]string
}

// WorkspaceDir is where a recording is processed: <root>/.work/<date>_<recording name>. It stays
// the same across runs (so results can be reused); the readable output folder is exported from it.
func WorkspaceDir(root, date, source string) string {
	name := filepath.Base(source)
	return filepath.Join(root, ".work", date+"_"+slug.Make(name[:len(name)-len(filepath.Ext(name))], 50))
}

// Prepare creates the workspace directories.
func (j *Job) Prepare() error {
	for _, dir := range []string{j.work(""), j.out(youtubeThumbs), j.out(filepath.Dir(podcastAudio)), j.out(clipsDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// Claude returns the shared client, created on first use so runs that never call Claude
// (e.g. with --start/--end and --only video) don't need the Claude Code CLI.
func (j *Job) Claude() (llm.Asker, error) {
	j.claudeOnce.Do(func() {
		client, err := llm.New(j.Config.Claude.Model, j.Config.Claude.Effort)
		if err != nil {
			j.claudeErr = err
			return
		}
		j.claude = client
	})
	return j.claude, j.claudeErr
}

// Tools and models, by the names stages list in uses().
const (
	ffmpeg      = "ffmpeg"
	whisperCLI  = "whisper-cli"
	whisperVAD  = "whisper-vad-speech-segments"
	sherpa      = "sherpa-onnx-offline-speaker-diarization"
	magick      = "magick"
	visionTool  = "sermon-vision"
	whisperFile = "model:whisper"
	vadFile     = "model:vad"
	segFile     = "model:segmentation"
	embedFile   = "model:embedding"
)

// Toolchain identifies the exact tools, helper build and model files in use, for run.json.
func (j *Job) Toolchain() map[string]string {
	j.toolchainOnce.Do(func() {
		ids := map[string]string{visionTool: fileID(j.Vision.Path)}
		for _, name := range []string{ffmpeg, whisperCLI, whisperVAD, sherpa, magick} {
			if path, err := exec.LookPath(name); err == nil {
				if real, err := filepath.EvalSymlinks(path); err == nil {
					path = real
				}
				ids[name] = path // Nix store paths name the exact build
			}
		}
		for key, file := range map[string]string{whisperFile: j.Models.Whisper, vadFile: j.Models.VAD, segFile: j.Models.Segmentation, embedFile: j.Models.Embedding} {
			ids[key] = fileID(file)
		}
		j.toolchain = ids
	})
	return j.toolchain
}

// uses returns the identities of the tools and models a stage depends on. They go into the
// stage's fingerprint, so after `nix flake update`, a rebuilt helper or a new model, exactly the
// stages that use it run again.
func (j *Job) uses(names ...string) map[string]string {
	all := j.Toolchain()
	ids := make(map[string]string, len(names))
	for _, n := range names {
		ids[n] = all[n]
	}
	return ids
}

// fileID identifies a file by path, size and modification time.
func fileID(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return path + " (missing)"
	}
	return path + "@" + info.ModTime().UTC().Format(time.RFC3339) + "/" + strconv.FormatInt(info.Size(), 10)
}

// Paths of every file the stages exchange.
func (j *Job) work(name string) string { return filepath.Join(j.Dir, "work", name) }
func (j *Job) out(name string) string  { return filepath.Join(j.Dir, "out", name) }

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Record is run.json: what this invocation did, with which tools, models and prompts, so any
// output can be traced back to what produced it.
type Record struct {
	Finished  string            `json:"finished"`
	Source    string            `json:"source"`
	Date      string            `json:"date"`
	Results   []pipeline.Result `json:"stages"`
	Toolchain map[string]string `json:"toolchain"`
	Prompts   map[string]string `json:"prompts"`
	Config    config.Config     `json:"config"`
}

// WriteRecord saves run.json in the workspace.
func (j *Job) WriteRecord(results []pipeline.Result) error {
	prompts := map[string]string{}
	for _, name := range promptNames {
		prompts[name] = llm.Load(name).Version
	}
	return writeJSON(filepath.Join(j.Dir, "run.json"), Record{
		Finished: time.Now().Format(time.RFC3339), Source: j.Source, Date: j.Date,
		Results: results, Toolchain: j.Toolchain(), Prompts: prompts, Config: j.Config,
	})
}

var promptNames = []string{"sermon-boundaries", "transcript-corrections", "descriptions", "clip-candidates", "clip-review"}
