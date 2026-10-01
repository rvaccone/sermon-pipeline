// Package vision runs the sermon-vision helper (Swift + Apple Vision) and reads its output.
package vision

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/rvaccone/sermon-pipeline/internal/command"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

// Box is [x, y, w, h] and Point is [x, y], normalized with a top-left origin.
type (
	Box   [4]float64
	Point [2]float64
)

func (b Box) CenterX() float64 { return b[0] + b[2]/2 }

// PoseFrame holds every candidate anchor for one video frame.
type PoseFrame struct {
	T      float64 `json:"t"`
	Necks  []Point `json:"necks"`
	Faces  []Box   `json:"faces"`
	Bodies []Box   `json:"bodies"`
}

// Face is a detected face with Vision's capture-quality score (0–1).
type Face struct {
	Box     Box     `json:"box"`
	Quality float64 `json:"quality"`
}

// FaceFrame is one frame sampled for thumbnail selection.
type FaceFrame struct {
	T     float64 `json:"t"`
	Faces []Face  `json:"faces"`
	Text  []Box   `json:"text"`
}

// Helper is the sermon-vision executable.
type Helper struct{ Path string }

// Find looks for sermon-vision next to the running program, then on PATH.
func Find() (Helper, error) {
	if exe, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(exe), "sermon-vision")
		if _, err := os.Stat(path); err == nil {
			return Helper{path}, nil
		}
	}
	if path, err := exec.LookPath("sermon-vision"); err == nil {
		return Helper{path}, nil
	}
	return Helper{}, fmt.Errorf("sermon-vision not found; run `make build`")
}

// Pose analyzes rate frames per second across span.
func (h Helper) Pose(ctx context.Context, video string, span timeline.Span, rate float64) ([]PoseFrame, error) {
	return run[PoseFrame](ctx, h, "pose", video, span, rate)
}

// Faces samples rate frames per second across span for face quality and on-screen text.
func (h Helper) Faces(ctx context.Context, video string, span timeline.Span, rate float64) ([]FaceFrame, error) {
	return run[FaceFrame](ctx, h, "faces", video, span, rate)
}

func run[T any](ctx context.Context, h Helper, cmd, video string, span timeline.Span, rate float64) ([]T, error) {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	out, err := command.Output(ctx, h.Path, cmd, video, "--start", f(span.Start), "--end", f(span.End), "--rate", f(rate))
	if err != nil {
		return nil, err
	}
	var records []T
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var r T
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("reading sermon-vision output: %w", err)
		}
		records = append(records, r)
	}
	return records, scanner.Err()
}
