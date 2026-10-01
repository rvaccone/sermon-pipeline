// Command sermon turns a recorded church service into publish-ready sermon outputs: the trimmed
// sermon video, podcast audio, captions, descriptions, clips, thumbnails and a review page.
// It never uploads anything.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/rvaccone/sermon-pipeline/internal/config"
	"github.com/rvaccone/sermon-pipeline/internal/models"
	"github.com/rvaccone/sermon-pipeline/internal/pipeline"
	"github.com/rvaccone/sermon-pipeline/internal/stages"
	"github.com/rvaccone/sermon-pipeline/internal/timeline"
	"github.com/rvaccone/sermon-pipeline/internal/vision"
)

const usage = `usage:
  sermon setup [--config config.toml]       download the AI models
  sermon run <recording> [options]          process one service
  sermon eval <labels.toml> [options]       score sermon detection against hand-picked times

run options:
  --config FILE      settings (default config.toml)
  --out DIR          where each sermon's output folder is created (default ~/Sermons)
  --date YYYY-MM-DD  service date (default: the recording's modification date)
  --preacher NAME    preacher's name, used in descriptions
  --start TIME       sermon start, e.g. 24:41 (with --end, skips detection)
  --end TIME         sermon end, e.g. 1:22:24
  --only STAGES      run only these stages and what they need, e.g. --only video,podcast
  --force STAGES     re-run these stages even if their results are current ("all" for every stage)
  --parallel N       stages allowed to run at once (default 3)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch os.Args[1] {
	case "setup":
		err = setup(ctx, os.Args[2:])
	case "run":
		err = run(ctx, os.Args[2:])
	case "eval":
		err = evaluate(ctx, os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sermon:", err)
		os.Exit(1)
	}
}

func setup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	configPath := fs.String("config", "config.toml", "")
	fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	set := models.Resolve(cfg.Transcription.Model)
	if err := set.Ensure(ctx, func(msg string) { fmt.Println(msg) }); err != nil {
		return err
	}
	fmt.Println("models ready in", models.Dir())
	return nil
}

// runOptions are the flags shared by run and eval.
type runOptions struct {
	config, out, date, preacher, start, end, only, force string
	parallel                                             int
}

func (o *runOptions) register(fs *flag.FlagSet) {
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	home, _ := os.UserHomeDir()
	fs.StringVar(&o.config, "config", "config.toml", "")
	fs.StringVar(&o.out, "out", filepath.Join(home, "Sermons"), "")
	fs.StringVar(&o.date, "date", "", "")
	fs.StringVar(&o.preacher, "preacher", "", "")
	fs.StringVar(&o.start, "start", "", "")
	fs.StringVar(&o.end, "end", "", "")
	fs.StringVar(&o.only, "only", "", "")
	fs.StringVar(&o.force, "force", "", "")
	fs.IntVar(&o.parallel, "parallel", 3, "")
}

func run(ctx context.Context, args []string) error {
	var o runOptions
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	o.register(fs)
	positional := parseInterspersed(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("give exactly one recording\n\n%s", usage)
	}
	job, err := newJob(positional[0], o)
	if err != nil {
		return err
	}
	results, runErr := runner(job, o).Run(ctx, job.Stages(), list(o.only))
	if err := job.WriteRecord(results); err != nil {
		slog.Warn("could not write run.json", "error", err)
	}
	printSummary(results, job)
	return runErr
}

// newJob validates the inputs and resolves everything a run needs.
func newJob(recording string, o runOptions) (*stages.Job, error) {
	cfg, err := config.Load(o.config)
	if err != nil {
		return nil, err
	}
	source, err := filepath.Abs(expandHome(recording))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	date := o.date
	if date == "" {
		date = info.ModTime().Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		return nil, fmt.Errorf("--date must be YYYY-MM-DD: %w", err)
	}
	override, err := parseOverride(o.start, o.end)
	if err != nil {
		return nil, err
	}
	set := models.Resolve(cfg.Transcription.Model)
	if missing := set.Missing(); len(missing) > 0 {
		return nil, fmt.Errorf("missing models (run `sermon setup`): %s", strings.Join(missing, ", "))
	}
	helper, err := vision.Find()
	if err != nil {
		return nil, err
	}
	job := &stages.Job{
		Config: cfg, Source: source, Date: date, Preacher: o.preacher, Override: override,
		Root: o.out, Dir: stages.WorkspaceDir(o.out, date, source), Models: set, Vision: helper,
	}
	return job, job.Prepare()
}

func runner(job *stages.Job, o runOptions) pipeline.Runner {
	force := map[string]bool{}
	for _, name := range list(o.force) {
		if name == "all" {
			for _, s := range job.Stages() {
				force[s.Name] = true
			}
			continue
		}
		force[name] = true
	}
	return pipeline.Runner{
		StatePath: filepath.Join(job.Dir, "state.json"),
		Log:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Parallel:  o.parallel,
		Force:     force,
	}
}

func parseOverride(start, end string) (*timeline.Span, error) {
	if start == "" && end == "" {
		return nil, nil
	}
	if start == "" || end == "" {
		return nil, fmt.Errorf("--start and --end go together")
	}
	s, err := timeline.Parse(start)
	if err != nil {
		return nil, fmt.Errorf("--start: %w", err)
	}
	e, err := timeline.Parse(end)
	if err != nil {
		return nil, fmt.Errorf("--end: %w", err)
	}
	if e <= s {
		return nil, fmt.Errorf("--end must be after --start")
	}
	return &timeline.Span{Start: s, End: e}, nil
}

func printSummary(results []pipeline.Result, job *stages.Job) {
	fmt.Println()
	for _, r := range results {
		line := fmt.Sprintf("  %-17s %-8s", r.Stage, r.Status)
		if r.Status == pipeline.Ran {
			line += fmt.Sprintf(" %5.0fs", r.Seconds)
		}
		fmt.Println(line)
	}
	if folder := job.OutputFolder(); folder != "" {
		fmt.Println("\nOutput folder:", folder)
		fmt.Println("Start with:   ", filepath.Join(folder, "Review.html"))
	}
}

// expandHome resolves a leading "~/" so paths copied from a shell or the labels file work.
func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}

// parseInterspersed lets flags come before or after the positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		fs.Parse(args)
		if fs.NArg() == 0 {
			return positional
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func list(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
