package church

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/rvaccone/sermon-pipeline/internal/llm"
)

// Details are what the website says about the church, as config.toml needs them.
type Details struct {
	Name      string   `json:"name" jsonschema_description:"The church's name as it presents itself, e.g. Feather Sound Church; empty if unclear"`
	Location  string   `json:"location" jsonschema_description:"City and state, e.g. Clearwater, FL; empty if the pages don't say"`
	Mission   string   `json:"mission" jsonschema_description:"The mission statement copied exactly; empty if there is none"`
	Podcast   string   `json:"podcast" jsonschema_description:"The podcast's name if the pages name one; otherwise empty"`
	Preachers []string `json:"preachers" jsonschema_description:"The people who preach, written as the site writes them with their title, lead pastor first"`
	Glossary  []string `json:"glossary" jsonschema_description:"Proper names a speech recognizer might mis-hear: ministries, programs, groups, network, local places, staff"`
	Notes     []string `json:"notes" jsonschema_description:"Anything a person should check; empty if nothing"`
}

// Learn asks Claude to read the pages and fill in the details. Claude sees only the pages' text.
func Learn(ctx context.Context, ask llm.Asker, site string, pages []Page) (Details, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Website: %s\n", site)
	for _, p := range pages {
		fmt.Fprintf(&b, "\n=== Page: %s (%s)\n%s\n", p.URL, p.Title, p.Text)
	}
	var d Details
	if err := ask.Ask(ctx, llm.Load("church-details"), b.String(), &d); err != nil {
		return d, err
	}
	d.Name = clean(d.Name)
	d.Location = clean(d.Location)
	d.Mission = clean(d.Mission)
	d.Podcast = clean(d.Podcast)
	d.Preachers = cleanAll(d.Preachers)
	d.Glossary = cleanAll(d.Glossary)
	return d, nil
}

// Config renders a starting config.toml. Values the website didn't give are left empty with a
// TODO, and the header says where everything came from so a person knows to check it.
func Config(d Details, domain, date string, pages []Page) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Settings for %s, filled in by `sermon init` on %s from these pages:\n", or(d.Name, "this church"), date)
	for _, p := range pages {
		fmt.Fprintf(&b, "#   %s\n", p.URL)
	}
	b.WriteString(`# Claude read the details below from the website: check each one. Anything not listed uses the
# defaults in internal/config/config.go; config.example.toml shows the other settings.

[church]
`)
	field := func(key, value, missing string) {
		fmt.Fprintf(&b, "%s = %s", key, quote(value))
		if value == "" {
			fmt.Fprintf(&b, " # TODO: %s", missing)
		}
		b.WriteString("\n")
	}
	field("name", d.Name, "the church's name (required)")
	field("website", domain, "the church's website")
	field("location", d.Location, `city and state, e.g. "Clearwater, FL"`)
	field("mission", d.Mission, "the mission statement, which sets the descriptions' voice")
	podcast := d.Podcast
	if podcast == "" {
		podcast = d.Name // the show name in the episode's tags; the church's name is the usual choice
	}
	field("podcast", podcast, "the podcast's name")
	b.WriteString(`# Who preaches here, as the church writes their names. The pipeline names the preacher from this
# list when the transcript identifies them, and restores these names when they're mis-heard.
`)
	list(&b, "preachers", d.Preachers)
	b.WriteString(`
[transcription]
# Names and terms the corrections stage may restore when the transcript mis-hears them. Claude
# proposes the fixes; only replacements that sound like the transcribed words are applied.
`)
	list(&b, "glossary", d.Glossary)
	b.WriteString(`
[sermon]
# "after-closing-prayer" includes the closing prayer and response; "after-teaching" stops
# when the teaching ends.
ends = "after-closing-prayer"
`)
	return b.String()
}

func list(b *strings.Builder, key string, values []string) {
	if len(values) == 0 {
		fmt.Fprintf(b, "%s = [] # TODO: none found on the website\n", key)
		return
	}
	fmt.Fprintf(b, "%s = [\n", key)
	for _, v := range values {
		fmt.Fprintf(b, "  %s,\n", quote(v))
	}
	b.WriteString("]\n")
}

// quote writes a TOML string. With control characters removed, Go's quoting is valid TOML.
func quote(s string) string {
	return strconv.Quote(clean(s))
}

// clean replaces control characters and collapses whitespace.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func cleanAll(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		v = clean(v)
		if v != "" && !seen[strings.ToLower(v)] {
			seen[strings.ToLower(v)] = true
			out = append(out, v)
		}
	}
	return out
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
