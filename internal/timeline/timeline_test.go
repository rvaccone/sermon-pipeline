package timeline

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]float64{
		"90":         90,
		"45:10":      45*60 + 10,
		"1:02:03":    3723,
		"01:02:03.5": 3723.5,
		" 0:00 ":     0,
		"24:41.25":   24*60 + 41.25,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1:2:3:4", "45:60", "1:75:00", "-5", "abc", "1::2"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestFormats(t *testing.T) {
	if got := Clock(3723.4); got != "1:02:03" {
		t.Errorf("Clock = %q", got)
	}
	if got := Clock(70); got != "1:10" {
		t.Errorf("Clock = %q", got)
	}
	if got := SRT(3723.456); got != "01:02:03,456" {
		t.Errorf("SRT = %q", got)
	}
	if got := ASS(3723.456); got != "1:02:03.46" {
		t.Errorf("ASS = %q", got)
	}
	if got := Precise(59.996); got != "0:01:00.00" {
		t.Errorf("Precise should carry rounding into the minutes, got %q", got)
	}
}

func TestSpan(t *testing.T) {
	a, b := Span{10, 20}, Span{15, 30}
	if a.Overlap(b) != 5 || b.Overlap(Span{40, 50}) != 0 {
		t.Error("Overlap")
	}
	if !a.Contains(10) || a.Contains(20) {
		t.Error("Contains is half-open")
	}
}
