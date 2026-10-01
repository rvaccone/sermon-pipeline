// Package review writes review.html: one local page showing everything the pipeline produced,
// with anything that needs a person's judgment at the top.
package review

import (
	"bytes"
	_ "embed"
	"html/template"
	"os"
)

// Page is everything the review page shows. Paths are relative to the page.
type Page struct {
	Title    string
	Church   string
	Date     string
	Flags    []string
	Sermon   Sermon
	Video    Media
	Podcast  Media
	Captions string
	Text     Text
	Clips    []Clip
	Rejected []string
	Thumbs   []string
	Fixes    []string // transcript corrections that were applied
	Refused  []string // corrections Claude proposed that failed the safety rule
}

type Sermon struct {
	Start, End, Length string
	StartQuote         string
	EndQuote           string
	Source             string // "claude" or "override"
	Confidence         string
	Reasoning          string
}

type Media struct {
	File     string
	Loudness string // e.g. "-14.0 LUFS · peak -1.6 dBTP"
}

type Text struct {
	Titles  []string
	YouTube string
	Podcast string
}

type Clip struct {
	File      string
	Title     string
	Caption   string
	Length    string
	Score     int
	Review    string
	Uncropped bool // tracking was not reliable, so the full frame is shown
}

//go:embed page.html
var pageTemplate string

var tmpl = template.Must(template.New("review").Parse(pageTemplate))

// Write renders the page to path.
func Write(path string, p Page) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, p); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
