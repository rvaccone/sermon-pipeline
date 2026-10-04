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
	FrameW  float64
	WindowW float64
	// Steadiness is how large a sway the camera ignores, as a share of the window's width: it
	// holds still through back-and-forth movement of about that size that reverses within a
	// second, and follows anything that goes somewhere.
	Steadiness float64
	// Smoothing is the Gaussian sigma, in seconds, that rounds off the start and end of each move.
	Smoothing float64
}

// minCoverage is the share of frames where the preacher must be found for tracking to be
// trusted; below it the clip uses the uncropped layout instead.
const minCoverage = 0.7

// Path returns the window's left edge, in pixels, for each of n output frames starting at
// clipStart, and whether tracking found the preacher often enough to use it.
//
// It follows the neck joint (steadier than a body box, which swings with gestures), falling back
// to the face, then the body. The window stays centered on him as he moves, with no lag, since
// the whole clip is known in advance. While he stands it holds still: his tracked position sways
// a little even then, and following that sway made the camera hunt left and right.
func (c Camera) Path(frames []vision.PoseFrame, clipStart float64, n int) ([]float64, bool) {
	times, xs := anchor(frames, clipStart)
	if len(xs) == 0 || float64(len(xs)) < minCoverage*float64(len(frames)) {
		return nil, false
	}
	xs = median(xs, 5)
	half := c.WindowW / 2
	subject := make([]float64, n) // where the window's center should be, in pixels, per frame
	for i := range subject {
		x := interpolate(times, xs, float64(i)/fps) * c.FrameW
		subject[i] = math.Max(half, math.Min(c.FrameW-half, x)) // the window can't leave the frame
	}
	// A sway of amplitude a that reverses every p frames is flattened when lambda ≳ a·p/2.
	lambda := c.Steadiness * c.WindowW * fps / 2
	centers := gaussian(flatten(subject, lambda), c.Smoothing*fps)
	left := make([]float64, n)
	for i, center := range centers {
		left[i] = math.Max(0, math.Min(c.FrameW-c.WindowW, center-half)) // sub-pixel; the render rounds
	}
	return left, true
}

// flatten removes back-and-forth wobble from xs while keeping real moves: it is the path c
// minimizing ½·Σ(c−x)² + lambda·Σ|Δc| (total-variation denoising). Because a move costs its
// distance, not its speed, a small wobble is cheaper to ignore than to follow, so the path is
// flat while he stands and sways, and follows him when he goes somewhere. A flat stretch of L
// frames between moves sits within about 2·lambda/L pixels of where he actually stands.
//
// It is solved by iteratively reweighted least squares: each round replaces |Δc| with a
// quadratic that touches it at the current path, which leaves a tridiagonal system.
func flatten(xs []float64, lambda float64) []float64 {
	n := len(xs)
	c := append([]float64(nil), xs...)
	if n < 2 || lambda <= 0 {
		return c
	}
	const floor = 0.01 // pixels; keeps the weights finite where the path is already flat
	diag, off := make([]float64, n), make([]float64, n-1)
	for round := 0; round < 300; round++ {
		for i := range diag {
			diag[i] = 1
		}
		for i := 0; i < n-1; i++ {
			w := lambda / math.Max(floor, math.Abs(c[i+1]-c[i]))
			diag[i] += w
			diag[i+1] += w
			off[i] = -w
		}
		next := solveTridiagonal(diag, off, xs)
		change := 0.0
		for i := range c {
			change = math.Max(change, math.Abs(next[i]-c[i]))
		}
		c = next
		if change < 0.01 {
			break
		}
	}
	return c
}

// solveTridiagonal solves the symmetric system with the given main and off diagonals (Thomas
// algorithm).
func solveTridiagonal(diag, off, y []float64) []float64 {
	n := len(y)
	d := append([]float64(nil), diag...)
	x := append([]float64(nil), y...)
	for i := 1; i < n; i++ {
		m := off[i-1] / d[i-1]
		d[i] -= m * off[i-1]
		x[i] -= m * x[i-1]
	}
	x[n-1] /= d[n-1]
	for i := n - 2; i >= 0; i-- {
		x[i] = (x[i] - off[i]*x[i+1]) / d[i]
	}
	return x
}

// gaussian smooths evenly spaced values with a Gaussian of sigma samples, looking both ways.
func gaussian(xs []float64, sigma float64) []float64 {
	reach := int(math.Ceil(3 * sigma))
	weights := make([]float64, reach+1)
	for k := range weights {
		weights[k] = math.Exp(-0.5 * math.Pow(float64(k)/sigma, 2))
	}
	out := make([]float64, len(xs))
	for i := range xs {
		var sum, total float64
		for j := max(0, i-reach); j <= min(len(xs)-1, i+reach); j++ {
			w := weights[abs(i-j)]
			sum += w * xs[j]
			total += w
		}
		out[i] = sum / total
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
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

// interpolate returns the subject's position at t, linearly between the nearest detections and
// held at the ends.
func interpolate(times, xs []float64, t float64) float64 {
	i := sort.SearchFloat64s(times, t)
	switch {
	case i == 0:
		return xs[0]
	case i == len(times):
		return xs[len(xs)-1]
	}
	if times[i] == times[i-1] {
		return xs[i]
	}
	f := (t - times[i-1]) / (times[i] - times[i-1])
	return xs[i-1] + f*(xs[i]-xs[i-1])
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
