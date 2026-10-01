// Package command runs the external programs the pipeline is built on (ffmpeg, whisper.cpp,
// sherpa-onnx, ImageMagick, the vision helper) and turns their failures into readable errors.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Run executes the program and discards its output. A failure includes the end of stderr.
func Run(ctx context.Context, name string, args ...string) error {
	_, _, err := run(ctx, "", name, args)
	return err
}

// RunIn is Run with dir as the working directory, so arguments can name files there by plain
// relative names (useful for ffmpeg filter arguments, which need escaping for some characters).
func RunIn(ctx context.Context, dir, name string, args ...string) error {
	_, _, err := run(ctx, dir, name, args)
	return err
}

// Output executes the program and returns its stdout.
func Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, _, err := run(ctx, "", name, args)
	return stdout, err
}

// Pipe executes the program in dir with stdin as its input and returns its stdout.
func Pipe(ctx context.Context, dir string, stdin io.Reader, name string, args ...string) ([]byte, error) {
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, stdin, &out, &errOut
	if err := check(ctx, name, cmd.Run(), errOut.String()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Stderr executes the program and returns its stderr. ffmpeg's analysis filters (loudnorm,
// silencedetect) report their results there.
func Stderr(ctx context.Context, name string, args ...string) ([]byte, error) {
	_, stderr, err := run(ctx, "", name, args)
	return stderr, err
}

func run(ctx context.Context, dir, name string, args []string) (stdout, stderr []byte, err error) {
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := check(ctx, name, cmd.Run(), errOut.String()); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), errOut.Bytes(), nil
}

// check turns a program's failure into a readable error.
func check(ctx context.Context, name string, err error, stderr string) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("%s is not installed or not on PATH (run inside `nix develop`)", name)
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return fmt.Errorf("%s failed: %w\n%s", name, err, tail(stderr, 12))
	}
}

// tail returns the last n non-empty lines, indented, for error messages.
func tail(text string, n int) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, "    "+line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
