package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/rvaccone/sermon-pipeline/internal/church"
	"github.com/rvaccone/sermon-pipeline/internal/config"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
)

// initConfig writes a starting config.toml from the church's website. It never overwrites one.
func initConfig(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	configPath := flags.String("config", "config.toml", "")
	positional := parseInterspersed(flags, args)
	if len(positional) != 1 {
		return fmt.Errorf("give the church's website, e.g. sermon init feathersoundchurch.com\n\n%s", usage)
	}
	if _, err := os.Stat(*configPath); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s already exists; move it aside, or pass --config to write a different file", *configPath)
	}
	home, err := church.Homepage(positional[0])
	if err != nil {
		return err
	}

	fmt.Println("Reading", home)
	pages, err := church.Read(ctx, home.String())
	if err != nil {
		return err
	}
	for _, p := range pages[1:] {
		fmt.Println("Reading", p.URL)
	}
	defaults := config.Default().Claude
	claude, err := llm.New(defaults.Model, defaults.Effort)
	if err != nil {
		return err
	}
	fmt.Println("Asking Claude for the church's details…")
	details, err := church.Learn(ctx, claude, home.String(), pages)
	if err != nil {
		return err
	}

	text := church.Config(details, church.Domain(home), time.Now().Format(time.DateOnly), pages)
	if err := os.WriteFile(*configPath, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Printf("\nWrote %s:\n\n%s\n", *configPath, text)
	for _, note := range details.Notes {
		fmt.Println("Check:", note)
	}
	if _, err := config.Load(*configPath); err != nil {
		fmt.Println("Before running, fix:", strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	fmt.Println("Next: check the values above, then run `bin/sermon setup`.")
	return nil
}
