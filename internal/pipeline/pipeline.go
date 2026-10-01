// Package pipeline runs stages in dependency order, in parallel where possible, and reuses a
// stage's earlier result when nothing it depends on has changed.
//
// A stage's fingerprint covers its name, version, settings (including the identity of the tools
// and models it uses), and the actual output files of the stages it reads. Upstream outputs, not upstream
// fingerprints, are what count: Claude's answers are not deterministic, so if re-running a stage
// produces different files, everything downstream of it runs again. Small files are compared by
// content, so an identical answer does not ripple downstream.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Stage is one step of the pipeline.
type Stage struct {
	Name    string
	Needs   []string
	Version int             // bump when the stage's logic changes, to discard old results
	Inputs  func() any      // settings that affect the result; hashed into the fingerprint
	Outputs func() []string // files the stage produces; checked before reusing a result
	Run     func(ctx context.Context) error

	// Finally stages run after every other selected stage has finished, even when some failed,
	// and read their outputs leniently. They are skipped only if one of their own Needs failed.
	Finally bool
}

// Result reports what happened to one stage.
type Result struct {
	Stage   string  `json:"stage"`
	Status  Status  `json:"status"`
	Seconds float64 `json:"seconds"`
	Error   string  `json:"error,omitempty"`
}

type Status string

const (
	Ran     Status = "ran"
	Reused  Status = "reused"
	Failed  Status = "failed"
	Skipped Status = "skipped" // a stage it needs failed
)

// Runner executes stages.
type Runner struct {
	StatePath string // where fingerprints are remembered between runs
	Log       *slog.Logger
	Parallel  int             // stages allowed to run at once
	Force     map[string]bool // stages to run even if a result could be reused
}

type entry struct {
	Fingerprint string `json:"fingerprint"`
	Outputs     string `json:"outputs"` // digest of the output files
}

// Run executes the target stages and everything they need (all stages if targets is empty).
// Independent stages keep going when one fails; only its dependents are skipped.
func (r Runner) Run(ctx context.Context, stages []Stage, targets []string) ([]Result, error) {
	byName, err := index(stages)
	if err != nil {
		return nil, err
	}
	for _, name := range append(append([]string(nil), targets...), keys(r.Force)...) {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("unknown stage %q (stages: %s)", name, names(stages))
		}
	}
	selected := selectStages(byName, stages, targets)
	state := r.loadState()

	var (
		mu      sync.Mutex
		results = map[string]Result{}
		done    = map[string]chan struct{}{}
		slots   = make(chan struct{}, max(1, r.Parallel))
		wg      sync.WaitGroup
	)
	for _, s := range selected {
		done[s.Name] = make(chan struct{})
	}
	finish := func(res Result) {
		mu.Lock()
		results[res.Stage] = res
		mu.Unlock()
	}
	for _, s := range selected {
		wg.Add(1)
		go func(s Stage) {
			defer wg.Done()
			defer close(done[s.Name])

			reads := s.Needs
			if s.Finally {
				reads = nil
				for _, other := range selected {
					if !other.Finally {
						reads = append(reads, other.Name)
					}
				}
			}
			var upstream []string
			for _, name := range reads {
				<-done[name]
				mu.Lock()
				res, outputs := results[name], state[name].Outputs
				mu.Unlock()
				blocked := res.Status == Failed || res.Status == Skipped
				if blocked && slices.Contains(s.Needs, name) {
					finish(Result{Stage: s.Name, Status: Skipped})
					r.Log.Warn("skipped", "stage", s.Name, "because", name+" did not finish")
					return
				}
				if blocked {
					outputs = "unavailable"
				}
				upstream = append(upstream, name+"="+outputs)
			}
			slots <- struct{}{}
			defer func() { <-slots }()

			fingerprint := r.fingerprint(s, upstream)
			mu.Lock()
			prev := state[s.Name]
			mu.Unlock()
			if !r.Force[s.Name] && prev.Fingerprint == fingerprint && prev.Outputs != "" && prev.Outputs == r.digest(s.Outputs) {
				finish(Result{Stage: s.Name, Status: Reused})
				r.Log.Info("reused", "stage", s.Name)
				return
			}

			r.Log.Info("running", "stage", s.Name)
			started := time.Now()
			err := s.Run(ctx)
			if err == nil {
				if missing := missingFiles(s.Outputs); len(missing) > 0 {
					err = fmt.Errorf("finished without writing %v", missing)
				}
			}
			res := Result{Stage: s.Name, Status: Ran, Seconds: time.Since(started).Seconds()}
			// A failed stage forgets its earlier result, so partial output is never reused.
			mu.Lock()
			if err != nil {
				res.Status, res.Error = Failed, err.Error()
				delete(state, s.Name)
			} else {
				state[s.Name] = entry{Fingerprint: fingerprint, Outputs: r.digest(s.Outputs)}
			}
			saveErr := r.saveState(state)
			mu.Unlock()
			if err != nil {
				r.Log.Error("failed", "stage", s.Name, "error", err)
			} else {
				r.Log.Info("finished", "stage", s.Name, "seconds", int(res.Seconds))
			}
			if saveErr != nil {
				r.Log.Warn("could not save pipeline state", "error", saveErr)
			}
			finish(res)
		}(s)
	}
	wg.Wait()

	ordered := make([]Result, 0, len(selected))
	var failures []error
	for _, s := range selected {
		res := results[s.Name]
		ordered = append(ordered, res)
		if res.Status == Failed {
			failures = append(failures, fmt.Errorf("%s: %s", s.Name, res.Error))
		}
	}
	return ordered, errors.Join(failures...)
}

// index checks that names are unique, every need exists, and there are no cycles.
func index(stages []Stage) (map[string]Stage, error) {
	byName := map[string]Stage{}
	for _, s := range stages {
		if _, dup := byName[s.Name]; dup {
			return nil, fmt.Errorf("pipeline: duplicate stage %q", s.Name)
		}
		byName[s.Name] = s
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(name string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("pipeline: dependency cycle through %q", name)
		}
		visiting[name] = true
		for _, need := range byName[name].Needs {
			if _, ok := byName[need]; !ok {
				return fmt.Errorf("pipeline: %q needs unknown stage %q", name, need)
			}
			if err := visit(need); err != nil {
				return err
			}
		}
		visiting[name], visited[name] = false, true
		return nil
	}
	for _, s := range stages {
		if err := visit(s.Name); err != nil {
			return nil, err
		}
	}
	return byName, nil
}

// selectStages returns the targets and everything they transitively need, in declaration order.
func selectStages(byName map[string]Stage, stages []Stage, targets []string) []Stage {
	if len(targets) == 0 {
		return stages
	}
	want := map[string]bool{}
	var add func(name string)
	add = func(name string) {
		if want[name] {
			return
		}
		want[name] = true
		for _, need := range byName[name].Needs {
			add(need)
		}
	}
	for _, t := range targets {
		add(t)
	}
	var out []Stage
	for _, s := range stages {
		if want[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

func (r Runner) fingerprint(s Stage, upstream []string) string {
	var inputs any
	if s.Inputs != nil {
		inputs = s.Inputs()
	}
	sort.Strings(upstream)
	data, _ := json.Marshal(struct {
		Name     string
		Version  int
		Inputs   any
		Upstream []string
	}{s.Name, s.Version, inputs, upstream})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// contentLimit is the size below which output files are compared by content; larger files
// (audio, video) are compared by size and modification time.
const contentLimit = 16 << 20

// digest summarizes a stage's output files. It is empty if any file is missing. Paths are taken
// relative to the state file's directory, so a workspace can be moved without losing its results.
func (r Runner) digest(outputs func() []string) string {
	h := sha256.New()
	base := filepath.Dir(r.StatePath)
	for _, path := range outputFiles(outputs) {
		info, err := os.Stat(path)
		if err != nil {
			return ""
		}
		name := path
		if rel, err := filepath.Rel(base, path); err == nil && !strings.HasPrefix(rel, "..") {
			name = rel
		}
		fmt.Fprintf(h, "%s|%d|", name, info.Size())
		if info.Size() > contentLimit {
			fmt.Fprintf(h, "%d\n", info.ModTime().UnixNano())
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return ""
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func missingFiles(outputs func() []string) []string {
	var missing []string
	for _, path := range outputFiles(outputs) {
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, path)
		}
	}
	return missing
}

func outputFiles(outputs func() []string) []string {
	if outputs == nil {
		return nil
	}
	return outputs()
}

func (r Runner) loadState() map[string]entry {
	state := map[string]entry{}
	if data, err := os.ReadFile(r.StatePath); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state
}

// saveState writes the state atomically, so an interruption never leaves a half-written file.
func (r Runner) saveState(state map[string]entry) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.StatePath), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.StatePath)
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func names(stages []Stage) string {
	var out []string
	for _, s := range stages {
		out = append(out, s.Name)
	}
	return strings.Join(out, ", ")
}
