package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/rvaccone/sermon-pipeline/internal/sermon"
	"github.com/rvaccone/sermon-pipeline/internal/stages"
)

// labels is the eval file: services with the sermon times a person chose.
type labels struct {
	Services []struct {
		Recording string `toml:"recording"`
		Start     string `toml:"start"`
		End       string `toml:"end"`
	} `toml:"service"`
}

// evaluate runs sermon detection on each labeled service and reports how far the automatic cut
// points are from the hand-picked ones. Rerun it after changing prompts, models or settings.
func evaluate(ctx context.Context, args []string) error {
	var o runOptions
	fs := flag.NewFlagSet("eval", flag.ExitOnError)
	o.register(fs)
	positional := parseInterspersed(fs, args)
	if len(positional) != 1 {
		return fmt.Errorf("give the labels file\n\n%s", usage)
	}
	var l labels
	if _, err := toml.DecodeFile(positional[0], &l); err != nil {
		return err
	}
	o.out = filepath.Join(o.out, "eval")
	o.start, o.end = "", ""

	var startErrs, endErrs []float64
	fmt.Printf("%-40s %12s %12s\n", "service", "start off", "end off")
	for _, svc := range l.Services {
		want, err := parseOverride(svc.Start, svc.End)
		if err == nil && want == nil {
			err = fmt.Errorf("start and end are required")
		}
		if err != nil {
			return fmt.Errorf("%s: %w", svc.Recording, err)
		}
		job, err := newJob(svc.Recording, o)
		if err != nil {
			return err
		}
		if _, err := runner(job, o).Run(ctx, job.Stages(), []string{stages.SermonStage}); err != nil {
			fmt.Printf("%-40s failed: %v\n", filepath.Base(svc.Recording), err)
			continue
		}
		var got sermon.Boundary
		data, err := os.ReadFile(filepath.Join(job.Dir, "work", "sermon.json"))
		if err == nil {
			err = json.Unmarshal(data, &got)
		}
		if err != nil {
			return err
		}
		ds, de := got.Span.Start-want.Start, got.Span.End-want.End
		startErrs, endErrs = append(startErrs, math.Abs(ds)), append(endErrs, math.Abs(de))
		fmt.Printf("%-40.40s %+11.1fs %+11.1fs\n", filepath.Base(svc.Recording), ds, de)
	}
	if len(startErrs) > 0 {
		fmt.Printf("\nmean absolute error: start %.1fs, end %.1fs over %d services\n",
			mean(startErrs), mean(endErrs), len(startErrs))
	}
	return nil
}

func mean(xs []float64) float64 {
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}
