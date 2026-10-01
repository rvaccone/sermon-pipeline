package terms

import (
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

func TestAllowed(t *testing.T) {
	allowed := Allowed([]string{"Healing Hearts", "Calvary Chapel", "Pastor Art Dykstra", "Art"})
	cases := []struct {
		find, replace string
		ok            bool
	}{
		// Real fixes.
		{"Matthew 24, 14", "Matthew 24:14", true},
		{"first Peter 2 4 through 10", "1 Peter 2:4-10", true},
		{"Romance 8, 28", "Romans 8:28", true},
		{"Matthew twenty four fourteen", "Matthew 24:14", true},
		{"healing parts ministry", "Healing Hearts ministry", true},
		{"Calvary chapel.", "Calvary Chapel.", true},
		{"Pastor Art Dikstra", "Pastor Art Dykstra", true},

		// Rewrites that must be refused.
		{"he said we must obey now", "Pastor Art Dykstra", false},                 // not similar
		{"the people said", "Mark 3", false},                                      // book and number never said
		{"chapter 7 verse 1", "Matthew 7:1", false},                               // book never said
		{"Kron 3", "Kron 3:1", false},                                             // not a book
		{"Matthew 24, 14", "Matthew 25:14", false},                                // number changed
		{"the healing parts were broken", "the Healing Hearts were fixed", false}, // sneaks in a change
		{"he went to Kron", "he went to Crohn", false},                            // not a glossary term
		{"this is fine", "this is great", false},                                  // rewording
		{"a party started", "a Art started", false},                               // must sound alike
		{"Amman", "Amman", false},                                                 // no change
	}
	for _, c := range cases {
		ok, why := allowed(transcript.Correction{Find: c.find, Replace: c.replace})
		if ok != c.ok {
			t.Errorf("%q → %q: allowed=%v (%s); want %v", c.find, c.replace, ok, why, c.ok)
		}
	}
}

func TestSpokenNumbers(t *testing.T) {
	got := spokenNumbers("first Peter chapter twenty four, verse 3")
	for _, n := range []int{1, 24, 3} {
		if !got[n] {
			t.Errorf("missing %d in %v", n, got)
		}
	}
	if got[20] || got[4] {
		t.Errorf("twenty four should read as 24 only, got %v", got)
	}
}
