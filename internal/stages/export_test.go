package stages

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/describe"
)

func TestExportName(t *testing.T) {
	text := describe.Text{Titles: []string{"Judging Without Hypocrisy | Matthew 7:1-6"}, Passage: "Matthew 7:1-6"}
	if got := exportName("2026-07-26", text); got != "2026-07-26 · Judging Without Hypocrisy (Matthew 7.1-6)" {
		t.Errorf("exportName = %q", got)
	}
	colon := describe.Text{Titles: []string{"Don't Judge: The Splinter and the Beam | Matthew 7:1-6"}, Passage: "Matthew 7:1-6"}
	if got := exportName("2026-07-26", colon); got != "2026-07-26 · Don't Judge - The Splinter and the Beam (Matthew 7.1-6)" {
		t.Errorf("exportName = %q", got)
	}
	if got := exportName("2026-07-26", describe.Text{}); got != "2026-07-26" {
		t.Errorf("exportName without a title = %q", got)
	}
}

func TestExportLinksRenamesAndCleansUp(t *testing.T) {
	root := t.TempDir()
	j := &Job{Root: root, Dir: filepath.Join(root, ".work", "job")}
	if err := j.Prepare(); err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(j.out(reviewPage), "page")
	write(j.clipFile("01-old"), "clip")
	if err := j.export("2026-07-26 · First Title"); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "2026-07-26 · First Title")
	write(filepath.Join(first, "my notes.txt"), "a person's own file")

	// The title changes and the clip selection changes.
	os.Remove(j.clipFile("01-old"))
	write(j.clipFile("01-new"), "clip")
	if err := j.export("2026-07-26 · Better Title"); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(root, "2026-07-26 · Better Title")
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Error("the old folder should have been renamed, not left behind")
	}
	for path, want := range map[string]bool{
		filepath.Join(second, clipsDir, "01-new.mp4"): true,
		filepath.Join(second, clipsDir, "01-old.mp4"): false,
		filepath.Join(second, "my notes.txt"):         true,
		filepath.Join(second, reviewPage):             true,
	} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", filepath.Base(path), err == nil, want)
		}
	}
}
