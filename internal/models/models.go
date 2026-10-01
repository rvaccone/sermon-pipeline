// Package models locates the local AI model files and downloads any that are missing.
package models

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Set is the resolved location of every model file the pipeline uses.
type Set struct {
	Whisper      string // whisper.cpp speech recognition weights
	VAD          string // Silero voice-activity detection, so whisper skips music and silence
	Segmentation string // pyannote speaker-change detection (sherpa-onnx)
	Embedding    string // TitaNet voice embeddings (sherpa-onnx)
}

type download struct {
	path    func(Set) string
	url     string
	archive bool // a .tar.bz2 that contains the file
}

// Dir is where models are cached: $XDG_CACHE_HOME/sermon-pipeline/models or ~/.cache/….
func Dir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "sermon-pipeline", "models")
}

// Resolve returns the model paths for the configured whisper model.
func Resolve(whisperModel string) Set {
	dir := Dir()
	return Set{
		Whisper:      filepath.Join(dir, whisperModel),
		VAD:          filepath.Join(dir, "ggml-silero-v5.1.2.bin"),
		Segmentation: filepath.Join(dir, "sherpa-onnx-pyannote-segmentation-3-0", "model.onnx"),
		Embedding:    filepath.Join(dir, "nemo_en_titanet_large.onnx"),
	}
}

func (s Set) downloads() []download {
	return []download{
		{func(s Set) string { return s.Whisper },
			"https://huggingface.co/ggerganov/whisper.cpp/resolve/main/" + filepath.Base(s.Whisper), false},
		{func(s Set) string { return s.VAD },
			"https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin", false},
		{func(s Set) string { return s.Segmentation },
			"https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-segmentation-models/sherpa-onnx-pyannote-segmentation-3-0.tar.bz2", true},
		{func(s Set) string { return s.Embedding },
			"https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/nemo_en_titanet_large.onnx", false},
	}
}

// Missing lists model files that are not on disk yet.
func (s Set) Missing() []string {
	var missing []string
	for _, d := range s.downloads() {
		if _, err := os.Stat(d.path(s)); err != nil {
			missing = append(missing, d.path(s))
		}
	}
	return missing
}

// Ensure downloads every missing model, reporting progress through log.
func (s Set) Ensure(ctx context.Context, log func(string)) error {
	for _, d := range s.downloads() {
		dest := d.path(s)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		log("downloading " + d.url)
		var err error
		if d.archive {
			if err = os.MkdirAll(Dir(), 0o755); err == nil {
				err = fetchArchive(ctx, d.url, filepath.Dir(filepath.Dir(dest)))
			}
		} else {
			err = fetchFile(ctx, d.url, dest)
		}
		if err != nil {
			return fmt.Errorf("downloading %s: %w", filepath.Base(dest), err)
		}
	}
	return nil
}

func open(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return resp.Body, nil
}

// fetchFile downloads to a temporary name first, so an interrupted download is never mistaken
// for a complete model.
func fetchFile(ctx context.Context, url, dest string) error {
	body, err := open(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	partial := dest + ".partial"
	f, err := os.Create(partial)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(partial, dest)
}

// fetchArchive downloads a .tar.bz2 and unpacks it into dir. It unpacks into a temporary
// directory first and moves the result into place only when complete, so an interrupted download
// never looks like a finished model.
func fetchArchive(ctx context.Context, url, dir string) error {
	body, err := open(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp, err := os.MkdirTemp(dir, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	var top []string
	archive := tar.NewReader(bzip2.NewReader(body))
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, header.Name)
		if !strings.HasPrefix(target, filepath.Clean(tmp)+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry escapes target directory: %s", header.Name)
		}
		if name, _, _ := strings.Cut(filepath.ToSlash(filepath.Clean(header.Name)), "/"); !slices.Contains(top, name) {
			top = append(top, name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeFile(target, archive)
		}
		if err != nil {
			return err
		}
	}
	for _, name := range top {
		if err := os.Rename(filepath.Join(tmp, name), filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
