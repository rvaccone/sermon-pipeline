package clips

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/command"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// Output canvas: vertical 1080×1920. The 4:5 window is scaled to 1080×1350 and placed near the
// top, leaving room for captions below; a blurred copy of the frame fills the background.
const (
	canvasW, canvasH = 1080, 1920
	windowTop        = 180
	captionMarginV   = 250
)

// WindowWidth is the 4:5 window's width for a frame of the given height.
func WindowWidth(frameH int) float64 { return float64(frameH) * 4 / 5 }

// RenderSpec describes one clip render.
type RenderSpec struct {
	ID         string        // file-name-safe clip ID
	Source     string        // original recording
	Span       timeline.Span // on the source timeline
	Audio      string        // mastered audio covering exactly Span
	FrameH     int
	Left       []float64 // window left edge per output frame; nil uses the uncropped layout
	Words      []transcript.Word
	FontFamily string // caption font, by family name
	FontDir    string // directory holding the caption font
	Preset     string
	CRF        int
	WorkDir    string // caption and camera files are written here, and ffmpeg runs here
	Dest       string
}

// Render draws captions, follows the camera path and encodes the clip once from the source.
// ffmpeg runs in WorkDir so the filter graph names its files by plain relative names.
func Render(ctx context.Context, s RenderSpec) error {
	assFile, cmdFile := s.ID+".ass", s.ID+".cmd"
	if err := os.WriteFile(filepath.Join(s.WorkDir, assFile), []byte(captionFile(s.Words, s.Span.Start, s.FontFamily)), 0o644); err != nil {
		return err
	}
	background := fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,gblur=sigma=40,eq=brightness=-0.2[bg]",
		canvasW, canvasH, canvasW, canvasH)
	foreground := fmt.Sprintf("[0:v]scale=%d:-2:flags=lanczos[fg]", canvasW)
	if s.Left != nil {
		if err := os.WriteFile(filepath.Join(s.WorkDir, cmdFile), []byte(cameraCommands(s.Left)), 0o644); err != nil {
			return err
		}
		// The crop is named so the camera commands reach it and not the background's crop.
		foreground = fmt.Sprintf("[0:v]sendcmd=f=%s,crop@cam=w=%d:h=%d:x=%d:y=0,scale=%d:-2:flags=lanczos[fg]",
			cmdFile, int(WindowWidth(s.FrameH)), s.FrameH, int(s.Left[0]), canvasW)
	}
	graph := fmt.Sprintf("%s;%s;[bg][fg]overlay=(W-w)/2:%d,ass=%s:fontsdir=%s[v]",
		background, foreground, windowTop, assFile, s.FontDir)

	return command.RunIn(ctx, s.WorkDir, "ffmpeg", "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-ss", timeline.Arg(s.Span.Start), "-t", timeline.Arg(s.Span.Duration()), "-i", s.Source,
		"-i", s.Audio,
		"-filter_complex", graph, "-map", "[v]", "-map", "1:a:0",
		"-c:v", "libx264", "-preset", s.Preset, "-crf", strconv.Itoa(s.CRF), "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", "-shortest", s.Dest)
}

// cameraCommands turns the path into ffmpeg sendcmd lines, one per change.
func cameraCommands(left []float64) string {
	var b strings.Builder
	last := -1.0
	for i, x := range left {
		if x != last {
			fmt.Fprintf(&b, "%.4f crop@cam x %d;\n", float64(i)/fps, int(x))
			last = x
		}
	}
	return b.String()
}
