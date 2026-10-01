package clips

import (
	"fmt"
	"strings"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/transcript"
)

// clipCues are short, punchy caption groups: up to three words or 16 characters.
var clipCues = transcript.CueLimits{MaxChars: 16, MaxWords: 3, MaxSeconds: 3, BreakPause: 0.45}

// captionFile builds an ASS subtitle file with word-by-word highlighting: each cue is shown once
// per word, with the word being spoken in gold.
func captionFile(words []transcript.Word, clipStart float64, font string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `[Script Info]
ScriptType: v4.00+
PlayResX: %d
PlayResY: %d
WrapStyle: 2

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Caption,%s,76,&H00FFFFFF,&H00FFFFFF,&H00000000,&H64000000,0,0,0,0,100,100,0,0,1,6,2,2,60,60,%d,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
`, canvasW, canvasH, font, captionMarginV)

	const gold, white = `{\c&H00D7FF&}`, `{\c&HFFFFFF&}`
	for _, cue := range transcript.Cues(words, clipCues) {
		for i, w := range cue.Words {
			end := cue.End
			if i+1 < len(cue.Words) {
				end = cue.Words[i+1].Start
			}
			parts := make([]string, len(cue.Words))
			for j, x := range cue.Words {
				parts[j] = strings.ToUpper(assText(x.Text))
				if j == i {
					parts[j] = gold + parts[j] + white
				}
			}
			fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Caption,,0,0,0,,%s\n",
				timeline.ASS(w.Start-clipStart), timeline.ASS(end-clipStart), strings.Join(parts, " "))
		}
	}
	return b.String()
}

// assText escapes characters ASS treats as markup.
func assText(s string) string {
	return strings.NewReplacer(`\`, `\\`, "{", "(", "}", ")").Replace(s)
}
