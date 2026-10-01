package command

import (
	"context"
	"strings"
	"testing"
)

func TestPipeReturnsOutput(t *testing.T) {
	out, err := Pipe(context.Background(), t.TempDir(), strings.NewReader("hello"), "cat")
	if err != nil || string(out) != "hello" {
		t.Errorf("Pipe = %q, %v", out, err)
	}
}

func TestFailureIncludesStderr(t *testing.T) {
	err := Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v; want stderr in the message", err)
	}
	if err := Run(context.Background(), "definitely-not-a-program"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("err = %v", err)
	}
}
