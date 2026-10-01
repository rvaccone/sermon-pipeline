// Package timeline holds the time types every stage shares: seconds on the source recording's
// timeline, spans of it, and the clock formats people read and type.
package timeline

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Span is a half-open interval [Start, End) in seconds.
type Span struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func (s Span) Duration() float64 { return s.End - s.Start }

func (s Span) Contains(t float64) bool { return t >= s.Start && t < s.End }

// Overlap returns how many seconds the two spans share.
func (s Span) Overlap(o Span) float64 {
	return math.Max(0, math.Min(s.End, o.End)-math.Max(s.Start, o.Start))
}

func (s Span) String() string { return Clock(s.Start) + "–" + Clock(s.End) }

var clockPattern = regexp.MustCompile(`^(?:(\d+):)?(?:(\d{1,2}):)?(\d{1,2}(?:\.\d+)?)$`)

// Parse reads "SS", "MM:SS" or "H:MM:SS", with optional fractional seconds.
func Parse(text string) (float64, error) {
	text = strings.TrimSpace(text)
	m := clockPattern.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("not a time: %q (use e.g. 45:10 or 1:02:03)", text)
	}
	// With two fields the regexp fills the first group, so shift it into minutes.
	hours, minutes := m[1], m[2]
	if minutes == "" && hours != "" {
		hours, minutes = "", hours
	}
	secs, _ := strconv.ParseFloat(m[3], 64)
	h, _ := strconv.Atoi(orZero(hours))
	mins, _ := strconv.Atoi(orZero(minutes))
	if (minutes != "" || hours != "") && secs >= 60 || hours != "" && mins >= 60 {
		return 0, fmt.Errorf("minutes and seconds must be under 60: %q", text)
	}
	return float64(h*3600+mins*60) + secs, nil
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// Clock formats seconds for people: "2:03" or "1:02:03", rounded to the second.
func Clock(seconds float64) string {
	total := int(math.Round(math.Max(0, seconds)))
	h, m, s := total/3600, total/60%60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// Precise formats seconds with hundredths, for logs and evidence: "1:02:03.45".
func Precise(seconds float64) string {
	cs := int(math.Round(math.Max(0, seconds) * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

// Arg formats seconds as a command-line argument with millisecond precision: "1481.250".
func Arg(seconds float64) string { return strconv.FormatFloat(seconds, 'f', 3, 64) }

// SRT formats seconds as an SRT timestamp: "01:02:03,450".
func SRT(seconds float64) string {
	ms := int(math.Round(math.Max(0, seconds) * 1000))
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// ASS formats seconds as an ASS subtitle timestamp: "1:02:03.45".
func ASS(seconds float64) string {
	cs := int(math.Round(math.Max(0, seconds) * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}
