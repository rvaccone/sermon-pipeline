package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEscapesAndShowsFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.html")
	err := Write(path, Page{
		Title: "Faithful <Under> Fire", Flags: []string{"Claude's confidence is low."},
		Sermon: Sermon{Source: "claude", Confidence: "low"},
		Clips:  []Clip{{File: "clips/01.mp4", Title: "One", Uncropped: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	html := string(data)
	for _, want := range []string{"Faithful &lt;Under&gt; Fire", "Check before publishing", "Claude&#39;s confidence is low.", "uncropped"} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}
