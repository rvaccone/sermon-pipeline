// Package slug turns titles and file names into short, safe path components.
package slug

import (
	"regexp"
	"strings"
)

var (
	apostrophe = regexp.MustCompile(`['’]`)
	nonSlug    = regexp.MustCompile(`[^a-z0-9]+`)
)

// Make lowercases s, drops apostrophes ("don't" becomes "dont"), joins words with hyphens, and shortens the result to at most limit bytes,
// ending at a whole word when it has to cut.
func Make(s string, limit int) string {
	out := apostrophe.ReplaceAllString(strings.ToLower(s), "")
	out = strings.Trim(nonSlug.ReplaceAllString(out, "-"), "-")
	if len(out) <= limit {
		return out
	}
	cut := out[:limit]
	if i := strings.LastIndex(cut, "-"); i > 0 && out[limit] != '-' {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, "-")
}
