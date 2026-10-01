package media

import (
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/timeline"
)

func TestParseSilences(t *testing.T) {
	log := `[silencedetect @ 0x1] silence_start: 1.5
[silencedetect @ 0x1] silence_end: 2.25 | silence_duration: 0.75
[silencedetect @ 0x1] silence_start: 8`
	got := parseSilences(log, 100, 110)
	want := []timeline.Span{{Start: 101.5, End: 102.25}, {Start: 108, End: 110}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseRate(t *testing.T) {
	if r := parseRate("30000/1001"); r < 29.97 || r > 29.98 {
		t.Errorf("parseRate = %v", r)
	}
	if parseRate("0/0") != 0 {
		t.Error("0/0 should be 0")
	}
}

func TestParseLoudness(t *testing.T) {
	report := []byte(`[Parsed_loudnorm_0 @ 0x1]
{
	"input_i" : "-19.57",
	"input_tp" : "-4.11",
	"input_lra" : "17.00",
	"input_thresh" : "-29.9"
}`)
	l, err := parseLoudness(report)
	if err != nil || l.Integrated != -19.57 || l.TruePeak != -4.11 || l.Range != 17 {
		t.Errorf("parseLoudness = %+v, %v", l, err)
	}
	silent := []byte(`{ "input_i" : "-inf", "input_tp" : "-inf", "input_lra" : "0.00" }`)
	if _, err := parseLoudness(silent); err == nil {
		t.Error("silent audio must be an error, not a gain of +inf dB")
	}
}
