package slug

import "testing"

func TestMake(t *testing.T) {
	if got := Make("The Sin We See | Matthew 7:1-6!", 40); got != "the-sin-we-see-matthew-7-1-6" {
		t.Errorf("Make = %q", got)
	}
	if got := Make("The Church's Mission Needs the Right Values", 40); got != "the-churchs-mission-needs-the-right" {
		t.Errorf("Make = %q; want it cut at a whole word", got)
	}
	if got := Make("Address the Behavior, Don’t Judge", 40); got != "address-the-behavior-dont-judge" {
		t.Errorf("Make = %q; want apostrophes dropped", got)
	}
	if got := Make("a very long title that keeps going and going", 12); got != "a-very-long" {
		t.Errorf("Make = %q", got)
	}
}
