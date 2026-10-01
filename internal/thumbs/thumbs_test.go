package thumbs

import (
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

func frame(t, quality, textArea float64) vision.FaceFrame {
	f := vision.FaceFrame{T: t, Faces: []vision.Face{{Box: vision.Box{0.5, 0.15, 0.07, 0.12}, Quality: quality}}}
	if textArea > 0 {
		f.Text = []vision.Box{{0.1, 0.1, textArea, 1}}
	}
	return f
}

func TestChoosePenalizesTextAndSpreadsPicks(t *testing.T) {
	frames := []vision.FaceFrame{
		frame(100, 0.50, 0.1), // best face, but slides behind him: 0.50 - 0.4 = 0.10
		frame(110, 0.40, 0),   // clean
		frame(130, 0.39, 0),   // clean but within a minute of 110
		frame(400, 0.30, 0),
	}
	got := Choose(frames, 2)
	if len(got) != 2 || got[0].T != 110 || got[1].T != 400 {
		t.Errorf("chose %+v; want 110 then 400", got)
	}
}

func TestFrameCropStaysInsideFrame(t *testing.T) {
	x, y, w, h := frameCrop(vision.Box{0.9, 0.05, 0.07, 0.12}, 1280, 720)
	if x < 0 || y < 0 || x+w > 1280 || y+h > 720 || w*9 != h*16 {
		t.Errorf("crop %d,%d %dx%d leaves the frame or is not 16:9", x, y, w, h)
	}
}
