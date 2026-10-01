package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type harness struct {
	dir  string
	mu   sync.Mutex
	runs map[string]int
}

func newHarness(t *testing.T) *harness {
	return &harness{dir: t.TempDir(), runs: map[string]int{}}
}

func (h *harness) runner() Runner {
	return Runner{StatePath: filepath.Join(h.dir, "state.json"), Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Parallel: 4}
}

// stage writes content() to <name>.txt and counts its runs.
func (h *harness) stage(name string, content func() string, needs ...string) Stage {
	out := filepath.Join(h.dir, name+".txt")
	return Stage{
		Name: name, Needs: needs,
		Outputs: func() []string { return []string{out} },
		Run: func(context.Context) error {
			h.mu.Lock()
			h.runs[name]++
			h.mu.Unlock()
			return os.WriteFile(out, []byte(content()), 0o644)
		},
	}
}

func constant(s string) func() string { return func() string { return s } }

func TestReusesUnchangedStages(t *testing.T) {
	h := newHarness(t)
	stages := []Stage{h.stage("a", constant("1")), h.stage("b", constant("2"), "a")}
	for i := 0; i < 2; i++ {
		if _, err := h.runner().Run(context.Background(), stages, nil); err != nil {
			t.Fatal(err)
		}
	}
	if h.runs["a"] != 1 || h.runs["b"] != 1 {
		t.Errorf("runs = %v; second run should reuse both", h.runs)
	}
}

func TestChangedUpstreamOutputRerunsDownstream(t *testing.T) {
	h := newHarness(t)
	answer := "first"
	stages := []Stage{h.stage("a", func() string { return answer }), h.stage("b", constant("x"), "a")}
	h.runner().Run(context.Background(), stages, nil)

	// Forcing "a" with a different (non-deterministic) answer must re-run "b".
	answer = "a different, longer answer"
	r := h.runner()
	r.Force = map[string]bool{"a": true}
	if _, err := r.Run(context.Background(), stages, nil); err != nil {
		t.Fatal(err)
	}
	if h.runs["a"] != 2 || h.runs["b"] != 2 {
		t.Errorf("runs = %v; b should re-run after a's output changed", h.runs)
	}
}

func TestFailureSkipsOnlyDependents(t *testing.T) {
	h := newHarness(t)
	failing := Stage{Name: "bad", Run: func(context.Context) error { return errors.New("boom") }}
	stages := []Stage{failing, h.stage("after-bad", constant("x"), "bad"), h.stage("independent", constant("y"))}
	results, err := h.runner().Run(context.Background(), stages, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	got := map[string]Status{}
	for _, r := range results {
		got[r.Stage] = r.Status
	}
	if got["bad"] != Failed || got["after-bad"] != Skipped || got["independent"] != Ran {
		t.Errorf("statuses = %v", got)
	}
}

func TestTargetsSelectDependencies(t *testing.T) {
	h := newHarness(t)
	stages := []Stage{h.stage("a", constant("1")), h.stage("b", constant("2"), "a"), h.stage("c", constant("3"))}
	results, _ := h.runner().Run(context.Background(), stages, []string{"b"})
	if len(results) != 2 || h.runs["c"] != 0 {
		t.Errorf("results = %+v; want only a and b", results)
	}
}

func TestRejectsCycles(t *testing.T) {
	h := newHarness(t)
	stages := []Stage{h.stage("a", constant("1"), "b"), h.stage("b", constant("2"), "a")}
	if _, err := h.runner().Run(context.Background(), stages, nil); err == nil {
		t.Error("want a cycle error")
	}
}

func TestIdenticalUpstreamContentIsReused(t *testing.T) {
	h := newHarness(t)
	stages := []Stage{h.stage("a", constant("same answer")), h.stage("b", constant("x"), "a")}
	h.runner().Run(context.Background(), stages, nil)
	r := h.runner()
	r.Force = map[string]bool{"a": true}
	r.Run(context.Background(), stages, nil)
	if h.runs["a"] != 2 || h.runs["b"] != 1 {
		t.Errorf("runs = %v; an identical answer from a must not re-run b", h.runs)
	}
}

func TestChangedInputsRerun(t *testing.T) {
	h := newHarness(t)
	tool := "ffmpeg 8"
	stage := h.stage("a", constant("1"))
	stage.Inputs = func() any { return tool }
	h.runner().Run(context.Background(), []Stage{stage}, nil)
	tool = "ffmpeg 9"
	h.runner().Run(context.Background(), []Stage{stage}, nil)
	if h.runs["a"] != 2 {
		t.Errorf("runs = %v; a changed tool identity must re-run the stage", h.runs)
	}
}

func TestUnknownStageNamesAreErrors(t *testing.T) {
	h := newHarness(t)
	stages := []Stage{h.stage("video", constant("1"))}
	if _, err := h.runner().Run(context.Background(), stages, []string{"vdeo"}); err == nil {
		t.Error("an unknown --only stage must be an error")
	}
	r := h.runner()
	r.Force = map[string]bool{"vdeo": true}
	if _, err := r.Run(context.Background(), stages, nil); err == nil {
		t.Error("an unknown --force stage must be an error")
	}
}

func TestFinallyRunsAfterFailures(t *testing.T) {
	h := newHarness(t)
	failing := Stage{Name: "clips", Run: func(context.Context) error { return errors.New("boom") }}
	review := h.stage("review", constant("page"), "video")
	review.Finally = true
	stages := []Stage{h.stage("video", constant("v")), failing, review}
	results, _ := h.runner().Run(context.Background(), stages, nil)
	got := map[string]Status{}
	for _, r := range results {
		got[r.Stage] = r.Status
	}
	if got["review"] != Ran || got["clips"] != Failed {
		t.Errorf("statuses = %v; review must still run after clips fails", got)
	}
}
