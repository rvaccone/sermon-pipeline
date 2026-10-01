// Package thumbs chooses the best frames of the preacher and composes YouTube thumbnails.
package thumbs

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/command"
	"github.com/rvaccone/sermon-pipeline/internal/media"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

// Candidate is a frame worth turning into a thumbnail.
type Candidate struct {
	T     float64    `json:"t"`
	Face  vision.Box `json:"face"`
	Score float64    `json:"score"`
}

const (
	minFaceHeight = 0.08 // smaller faces read poorly at thumbnail size
	textPenalty   = 4.0  // score lost per unit of frame area covered by on-screen text
	minGap        = 60.0 // seconds between chosen frames, for variety
)

// Choose returns up to count frames ranked by face quality, penalizing on-screen text (slides
// behind the preacher make cluttered thumbnails) and keeping picks at least a minute apart.
func Choose(frames []vision.FaceFrame, count int) []Candidate {
	var all []Candidate
	for _, f := range frames {
		face, ok := bestFace(f.Faces)
		if !ok {
			continue
		}
		textArea := 0.0
		for _, b := range f.Text {
			textArea += b[2] * b[3]
		}
		all = append(all, Candidate{T: f.T, Face: face.Box, Score: face.Quality - textPenalty*textArea})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	var chosen []Candidate
	for _, c := range all {
		if len(chosen) == count {
			break
		}
		if !nearAny(c.T, chosen) {
			chosen = append(chosen, c)
		}
	}
	return chosen
}

func bestFace(faces []vision.Face) (vision.Face, bool) {
	best, ok := vision.Face{}, false
	for _, f := range faces {
		if f.Box[3] >= minFaceHeight && (!ok || f.Quality > best.Quality) {
			best, ok = f, true
		}
	}
	return best, ok
}

func nearAny(t float64, chosen []Candidate) bool {
	for _, c := range chosen {
		if math.Abs(c.T-t) < minGap {
			return true
		}
	}
	return false
}

// Spec describes one thumbnail.
type Spec struct {
	Source  string
	Width   int // source frame size
	Height  int
	Frame   Candidate
	Title   string
	Passage string
	Font    string // font file for the title and passage
	Dest    string // JPEG
}

// Compose crops a 16:9 frame around the preacher (placed on the right third), scales it to
// 1280×720, darkens the left side and sets the title and passage there.
func Compose(ctx context.Context, s Spec) error {
	x, y, w, h := frameCrop(s.Frame.Face, float64(s.Width), float64(s.Height))
	still := strings.TrimSuffix(s.Dest, ".jpg") + "-frame.png"
	defer os.Remove(still)
	if err := media.FFmpeg(ctx, "-ss", timeline.Arg(s.Frame.T), "-i", s.Source, "-frames:v", "1",
		"-vf", fmt.Sprintf("crop=%d:%d:%d:%d,scale=1280:720:flags=lanczos,unsharp=5:5:0.5", w, h, x, y),
		still); err != nil {
		return err
	}
	return command.Run(ctx, "magick", still,
		// Shade from 92% black at the left edge to clear at 1100 px; the title box ends at 620 px,
		// where the shade is still about 40%, and the text's dark outline covers the rest.
		"(", "-size", "1100x720", "-define", "gradient:direction=east",
		"gradient:rgba(0,0,0,0.92)-rgba(0,0,0,0)", "-background", "none", "-extent", "1280x720", ")",
		"-compose", "over", "-composite",
		"(", "-size", "560x420", "-background", "none", "-fill", "white",
		"-stroke", "rgba(0,0,0,0.55)", "-strokewidth", "2", "-font", s.Font,
		"-gravity", "southwest", "caption:"+magickText(strings.ToUpper(s.Title)), ")",
		"-gravity", "northwest", "-geometry", "+60+150", "-composite",
		"-font", s.Font, "-fill", "#FFD700", "-pointsize", "44", "-annotate", "+64+620", magickText(s.Passage),
		"-quality", "92", s.Dest)
}

// magickText escapes text for ImageMagick, which treats % as a format code and a leading @ as a
// file to read.
func magickText(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	if strings.HasPrefix(s, "@") {
		s = `\` + s
	}
	return s
}

// frameCrop sizes a 16:9 crop to the face (about 5.5 face-heights tall, so head and torso fit)
// and positions it with the face at 68% across and 18% down, clamped to the frame.
func frameCrop(face vision.Box, frameW, frameH float64) (x, y, w, h int) {
	tall := math.Min(frameH, math.Max(frameH/2, face[3]*frameH*5.5))
	// A width that is a multiple of 32 gives an exact 16:9 crop with even dimensions.
	w = int(math.Min(frameW, tall*16/9)) / 32 * 32
	h = w * 9 / 16
	cx := face.CenterX()*frameW - 0.68*float64(w)
	cy := face[1]*frameH - 0.18*float64(h)
	x = int(math.Max(0, math.Min(frameW-float64(w), cx))) &^ 1
	y = int(math.Max(0, math.Min(frameH-float64(h), cy))) &^ 1
	return x, y, w, h
}
