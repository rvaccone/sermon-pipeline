package clips

import (
	"math"
	"sort"

	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

// fps is the clips' output frame rate; the camera position is planned for every output frame.
const fps = 30.0

// FrameCount is how many output frames a clip of duration d seconds has.
func FrameCount(d float64) int { return int(d*fps + 0.5) }

// Camera describes the virtual camera: a window of WindowW pixels sliding across a frame of
// FrameW pixels, keeping the preacher centered.
type Camera struct {
	FrameW    float64
	WindowW   float64
	Smoothing float64 // Gaussian sigma in seconds; larger is calmer, smaller hugs him tighter
}

// minCoverage is the share of frames where the preacher must be found for tracking to be
// trusted; below it the clip uses the uncropped layout instead.
const minCoverage = 0.7

// Path returns the window's left edge, in pixels, for each of n output frames starting at
// clipStart, and whether tracking found the preacher often enough to use it.
//
// It follows the neck joint (steadier than a body box, which swings with gestures), falling back
// to the face, then the body. There is no dead zone: the window is always centered on a
// Gaussian-smoothed position that looks both backward and forward in time, so it glides with him
// instead of holding still and then jumping.
func (c Camera) Path(frames []vision.PoseFrame, clipStart float64, n int) ([]float64, bool) {
	times, xs := anchor(frames, clipStart)
	if len(xs) == 0 || float64(len(xs)) < minCoverage*float64(len(frames)) {
		return nil, false
	}
	xs = median(xs, 5)
	left := make([]float64, n)
	for i := range left {
		center := smoothAt(times, xs, float64(i)/fps, c.Smoothing)
		left[i] = math.Round(math.Max(0, math.Min(c.FrameW-c.WindowW, center*c.FrameW-c.WindowW/2)))
	}
	return left, true
}

// anchor picks, for each frame, the horizontal position of the person closest to where the
// preacher was last seen. The first frame starts from the median position of everyone detected
// across the clip, which is the person present most consistently rather than whoever Vision
// happened to list first.
func anchor(frames []vision.PoseFrame, clipStart float64) (times, xs []float64) {
	perFrame := make([][]float64, len(frames))
	var everyone []float64
	for i, f := range frames {
		perFrame[i] = candidates(f)
		everyone = append(everyone, perFrame[i]...)
	}
	if len(everyone) == 0 {
		return nil, nil
	}
	sort.Float64s(everyone)
	prev := everyone[len(everyone)/2]
	for i, f := range frames {
		if len(perFrame[i]) == 0 {
			continue
		}
		best := perFrame[i][0]
		for _, x := range perFrame[i][1:] {
			if math.Abs(x-prev) < math.Abs(best-prev) {
				best = x
			}
		}
		prev = best
		times = append(times, f.T-clipStart)
		xs = append(xs, best)
	}
	return times, xs
}

// candidates returns everyone's horizontal position in a frame, preferring neck joints (steady
// through gestures), then faces, then body boxes.
func candidates(f vision.PoseFrame) []float64 {
	var xs []float64
	for _, p := range f.Necks {
		xs = append(xs, p[0])
	}
	if len(xs) == 0 {
		for _, b := range f.Faces {
			xs = append(xs, b.CenterX())
		}
	}
	if len(xs) == 0 {
		for _, b := range f.Bodies {
			xs = append(xs, b.CenterX())
		}
	}
	return xs
}

// smoothAt is a Gaussian-weighted average of xs around t, using samples within 3 sigma.
func smoothAt(times, xs []float64, t, sigma float64) float64 {
	var sum, weights float64
	for j := sort.SearchFloat64s(times, t-3*sigma); j < len(times) && times[j] <= t+3*sigma; j++ {
		w := math.Exp(-0.5 * math.Pow((times[j]-t)/sigma, 2))
		sum += w * xs[j]
		weights += w
	}
	if weights == 0 {
		return nearest(times, xs, t)
	}
	return sum / weights
}

func nearest(times, xs []float64, t float64) float64 {
	i := sort.SearchFloat64s(times, t)
	switch {
	case i == 0:
		return xs[0]
	case i == len(times):
		return xs[len(xs)-1]
	case t-times[i-1] < times[i]-t:
		return xs[i-1]
	default:
		return xs[i]
	}
}

// median replaces each value with the median of its k-wide neighborhood, removing one-off
// detection glitches.
func median(xs []float64, k int) []float64 {
	out := make([]float64, len(xs))
	for i := range xs {
		window := append([]float64(nil), xs[max(0, i-k/2):min(len(xs), i+k/2+1)]...)
		sort.Float64s(window)
		out[i] = window[len(window)/2]
	}
	return out
}
