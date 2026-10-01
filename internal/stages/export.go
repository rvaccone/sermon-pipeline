package stages

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/describe"
)

// exportRecord is work/export.json: the output folder this recording was last exported to.
type exportRecord struct {
	Folder string `json:"folder"`
}

// managed are the top-level entries of an output folder that the pipeline owns. Anything else a
// person puts in the folder is left alone.
var managed = []string{reviewPage, transcriptText, "YouTube", "Podcast", clipsDir}

// exportName is the output folder's name, e.g. "2026-07-26 · Judging Without Hypocrisy
// (Matthew 7.1-6)", so each sermon is easy to find. Characters Finder can't show are replaced.
func exportName(date string, text describe.Text) string {
	name := date
	if len(text.Titles) > 0 {
		title, _, _ := strings.Cut(text.Titles[0], " | ")
		name += " · " + strings.TrimSpace(title)
	}
	if text.Passage != "" {
		name += " (" + text.Passage + ")"
	}
	name = strings.NewReplacer(":", ".", "/", "-", "\\", "-").Replace(name)
	if r := []rune(name); len(r) > 120 {
		name = strings.TrimSpace(string(r[:120]))
	}
	return name
}

// export mirrors the deliverables into <Root>/<name>. Files are hard-linked (no extra disk space for
// the videos). A previous export under another name is renamed, not duplicated, and stale files
// the pipeline manages (an old clip or thumbnail) are removed.
func (j *Job) export(name string) error {
	dest := filepath.Join(j.Root, name)
	var prev exportRecord
	if readJSON(j.work("export.json"), &prev) == nil && prev.Folder != "" && prev.Folder != dest {
		if _, err := os.Stat(dest); errors.Is(err, fs.ErrNotExist) {
			if err := os.Rename(prev.Folder, dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}

	wanted := map[string]bool{}
	err := filepath.WalkDir(j.out(""), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(j.out(""), path)
		wanted[rel] = true
		return link(path, filepath.Join(dest, rel))
	})
	if err != nil {
		return err
	}
	if err := removeUnwanted(dest, wanted); err != nil {
		return err
	}
	return writeJSON(j.work("export.json"), exportRecord{Folder: dest})
}

// OutputFolder is the readable output folder from the last export, or "" if there is none yet.
func (j *Job) OutputFolder() string {
	var rec exportRecord
	if readJSON(j.work("export.json"), &rec) != nil {
		return ""
	}
	return rec.Folder
}

// exportFiles are the review stage's outputs: the page and the exported folder's copy of it, so
// deleting the output folder makes the next run export it again.
func (j *Job) exportFiles() []string {
	files := []string{j.out(reviewPage), j.work("export.json")}
	var rec exportRecord
	if readJSON(j.work("export.json"), &rec) == nil && rec.Folder != "" {
		files = append(files, filepath.Join(rec.Folder, reviewPage))
	}
	return files
}

// link makes dst a hard link to src, falling back to a copy across file systems.
func link(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// removeUnwanted deletes files under the managed entries of dest that are no longer deliverables.
func removeUnwanted(dest string, wanted map[string]bool) error {
	for _, entry := range managed {
		root := filepath.Join(dest, entry)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dest, path)
			if !wanted[rel] {
				return os.Remove(path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
