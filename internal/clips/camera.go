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
// FrameW pixels, following the preacher the way a camera operator would.
type Camera struct {
	FrameW  float64
	WindowW float64
	// Tolerance is how far the preacher may drift from the window's center, as a share of its
	// width, before the camera moves. It absorbs swaying and gesturing.
	Tolerance float64
	// Smoothing is roughly how many seconds the camera takes to ease into or out of a move.
	Smoothing float64
}

// minCoverage is the share of frames where the preacher must be found for tracking to be
// trusted; below it the clip uses the uncropped layout instead.
const minCoverage = 0.7

// Path returns the window's left edge, in pixels, for each of n output frames starting at
// clipStart, and whether tracking found the preacher often enough to use it.
//
// It follows the neck joint (steadier than a body box, which swings with gestures), falling back
// to the face, then the body. The whole path is planned at once, since the clip is known in
// advance: it is the smoothest path that keeps the preacher within Tolerance of center. So the
// camera holds still while he stands and sways, eases into a pan when he walks, and eases out
// again, never jumping.
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
	centers := steady(subject, c.Tolerance*c.WindowW, c.Smoothing*fps, recenter*fps)
	left := make([]float64, n)
	for i, center := range centers {
		left[i] = math.Max(0, math.Min(c.FrameW-c.WindowW, center-half)) // sub-pixel; the render rounds
	}
	return left, true
}

// Weights of the camera plan, relative to centering (weight 1): drifting past the tolerance
// costs far more than any smoothness term, so it behaves almost like a hard limit.
const outsideWeight = 1000

// recenter is roughly how many seconds the camera takes to drift back to center while the
// preacher stands within the tolerance: slow enough that nobody sees it move.
const recenter = 6.0

// steady plans the camera's center for each frame. It minimizes, over the whole clip:
//
//	Σ (c−s)²  +  outsideWeight·Σ max(0, |c−s|−tol)²  +  λ₁·Σ (Δc)²  +  λ₂·Σ (Δ²c)²
//
// where s is the subject's position: a weak pull toward the subject that recenters him slowly
// while he stands, a strong one wherever he would drift past the tolerance, and penalties on the
// camera's speed and acceleration that make it hold still and move in smooth, eased pans. Both
// time scales are in frames: drift sets how slowly the weak pull recenters him (λ₁ = drift²),
// and ease how gradually a pan starts and stops against the strong pull (λ₂ = outsideWeight·ease⁴).
// The objective is convex; each round fixes which frames sit past the tolerance and solves the
// resulting linear system, until that set stops changing.
func steady(s []float64, tol, ease, drift float64) []float64 {
	n := len(s)
	if n < 3 {
		return append([]float64(nil), s...)
	}
	lambda1, lambda2 := drift*drift, outsideWeight*math.Pow(ease, 4)
	c := append([]float64(nil), s...)
	side := make([]int, n) // −1 or +1 where the camera sits past the tolerance, else 0
	for round := 0; round < 50; round++ {
		band := make([][3]float64, n) // band[i][k] = A(i, i−k)
		b := make([]float64, n)
		for i := range s {
			band[i][0], b[i] = 1, s[i]
			if side[i] != 0 {
				edge := s[i] + float64(side[i])*tol
				band[i][0] += outsideWeight
				b[i] += outsideWeight * edge
			}
		}
		addDifferences(band, lambda1, []float64{-1, 1})
		addDifferences(band, lambda2, []float64{1, -2, 1})
		c = solveBand(band, b)

		changed := false
		for i := range s {
			next := 0
			switch {
			case c[i] > s[i]+tol:
				next = 1
			case c[i] < s[i]-tol:
				next = -1
			}
			if next != side[i] {
				side[i], changed = next, true
			}
		}
		if !changed {
			break
		}
	}
	return c
}

// addDifferences adds λ·DᵀD to the band matrix, where each row of D applies coefs to
// consecutive frames (e.g. −1, 1 for speed).
func addDifferences(band [][3]float64, lambda float64, coefs []float64) {
	for start := 0; start+len(coefs) <= len(band); start++ {
		for a := range coefs {
			for b := 0; b <= a; b++ {
				band[start+a][a-b] += lambda * coefs[a] * coefs[b]
			}
		}
	}
}

// solveBand solves A·x = y for a symmetric positive-definite matrix with two diagonals on each
// side of the main one, given as band[i][k] = A(i, i−k), by banded Cholesky factorization.
func solveBand(band [][3]float64, y []float64) []float64 {
	n := len(y)
	l := make([][3]float64, n) // l[i][k] = L(i, i−k)
	for i := 0; i < n; i++ {
		for k := min(2, i); k >= 0; k-- {
			j := i - k
			sum := band[i][k]
			for m := max(0, i-2); m < j; m++ {
				sum -= l[i][i-m] * l[j][j-m]
			}
			if k == 0 {
				l[i][0] = math.Sqrt(sum)
			} else {
				l[i][k] = sum / l[j][0]
			}
		}
	}
	x := make([]float64, n)
	for i := 0; i < n; i++ { // L·z = y
		sum := y[i]
		for m := max(0, i-2); m < i; m++ {
			sum -= l[i][i-m] * x[m]
		}
		x[i] = sum / l[i][0]
	}
	for i := n - 1; i >= 0; i-- { // Lᵀ·x = z
		sum := x[i]
		for m := i + 1; m <= min(n-1, i+2); m++ {
			sum -= l[m][m-i] * x[m]
		}
		x[i] = sum / l[i][0]
	}
	return x
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
