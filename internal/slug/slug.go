// Package slug turns titles and file names into short, safe path components.
package slug

import (
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Make lowercases s, joins words with hyphens, and shortens the result to at most limit bytes,
// ending at a whole word when it has to cut.
func Make(s string, limit int) string {
	out := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) <= limit {
		return out
	}
	cut := out[:limit]
	if i := strings.LastIndex(cut, "-"); i > 0 && out[limit] != '-' {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, "-")
}
