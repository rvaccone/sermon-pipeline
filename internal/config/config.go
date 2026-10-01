// Package config loads config.toml: the church's details and every tunable setting, so nothing
// about a particular church or machine is hard-coded.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Church        Church        `toml:"church"`
	Transcription Transcription `toml:"transcription"`
	Sermon        Sermon        `toml:"sermon"`
	Claude        Claude        `toml:"claude"`
	Audio         Audio         `toml:"audio"`
	Video         Video         `toml:"video"`
	Clips         Clips         `toml:"clips"`
	Thumbnails    Thumbnails    `toml:"thumbnails"`
}

type Church struct {
	Name     string `toml:"name"`
	Location string `toml:"location"` // e.g. "Clearwater, FL"; helps local search
	Website  string `toml:"website"`
	Mission  string `toml:"mission"`
	Podcast  string `toml:"podcast"` // podcast/show name used in audio tags
}

type Transcription struct {
	Model    string   `toml:"model"`    // whisper.cpp model file name in the models directory
	Language string   `toml:"language"` // e.g. "en"
	Glossary []string `toml:"glossary"` // names and terms the corrections stage may restore
}

type Sermon struct {
	// Where the sermon ends: "after-closing-prayer" or "after-teaching".
	Ends string `toml:"ends"`
}

type Claude struct {
	Model  string `toml:"model"`
	Effort string `toml:"effort"` // low | medium | high | xhigh | max
}

type Audio struct {
	VideoLUFS      float64 `toml:"video_lufs"`
	PodcastLUFS    float64 `toml:"podcast_lufs"`
	TruePeak       float64 `toml:"true_peak"` // dBTP ceiling after encoding
	PodcastBitrate string  `toml:"podcast_bitrate"`
}

type Video struct {
	Preset string `toml:"preset"` // x264 preset
	CRF    int    `toml:"crf"`
}

type Clips struct {
	Count            int     `toml:"count"`
	MinScore         int     `toml:"min_score"` // the reviewer's 1-10 score a clip needs to be kept
	MinSeconds       float64 `toml:"min_seconds"`
	MaxSeconds       float64 `toml:"max_seconds"`
	CRF              int     `toml:"crf"`
	SmoothingSeconds float64 `toml:"smoothing_seconds"` // camera smoothing; larger is calmer
	FontFile         string  `toml:"font_file"`         // caption font; its directory is searched
	FontFamily       string  `toml:"font_family"`       // the font's family name, as captions refer to it
}

type Thumbnails struct {
	Count    int    `toml:"count"`
	FontFile string `toml:"font_file"`
}

// Default returns every setting the probe validated. config.toml only needs to override.
func Default() Config {
	return Config{
		// large-v3-turbo, not large-v3: on this church's recordings large-v3 fell into repetition
		// loops in every full-length run (one lost 18 minutes), while turbo produced none, with the
		// same word count and punctuation in less than half the time.
		Transcription: Transcription{Model: "ggml-large-v3-turbo.bin", Language: "en"},
		Sermon:        Sermon{Ends: "after-closing-prayer"},
		Claude:        Claude{Model: "claude-opus-5-5", Effort: "high"},
		Audio:         Audio{VideoLUFS: -14, PodcastLUFS: -16, TruePeak: -1.5, PodcastBitrate: "192k"},
		Video:         Video{Preset: "slow", CRF: 13},
		Clips: Clips{
			Count: 3, MinScore: 6, MinSeconds: 25, MaxSeconds: 90, CRF: 16, SmoothingSeconds: 0.3,
			FontFile: "/System/Library/Fonts/Supplemental/Arial Black.ttf", FontFamily: "Arial Black",
		},
		Thumbnails: Thumbnails{Count: 4, FontFile: "/System/Library/Fonts/Supplemental/Arial Black.ttf"},
	}
}

// Load reads path on top of the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return cfg, fmt.Errorf("%s: unknown setting %q", path, undecoded[0].String())
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(c.Church.Name != "", "church.name is required")
	check(c.Sermon.Ends == "after-closing-prayer" || c.Sermon.Ends == "after-teaching",
		"sermon.ends must be \"after-closing-prayer\" or \"after-teaching\"")
	check(map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}[c.Claude.Effort],
		"claude.effort must be low, medium, high, xhigh or max")
	check(c.Audio.TruePeak < 0, "audio.true_peak must be below 0 dBTP")
	check(c.Clips.Count >= 0 && c.Clips.MinSeconds > 0 && c.Clips.MaxSeconds > c.Clips.MinSeconds,
		"clips: count must be >= 0 and min_seconds < max_seconds")
	check(c.Clips.SmoothingSeconds > 0, "clips.smoothing_seconds must be positive")
	check(c.Clips.FontFamily != "" && !strings.ContainsAny(c.Clips.FontFamily, ","),
		"clips.font_family is required and cannot contain a comma")
	// The caption font's directory goes inside an ffmpeg filter graph, where these characters
	// would need escaping; keeping them out keeps the graph simple.
	check(!strings.ContainsAny(filepath.Dir(c.Clips.FontFile), `:,;[]'\`),
		"clips.font_file's directory cannot contain : , ; [ ] ' or \\")
	for _, font := range []string{c.Clips.FontFile, c.Thumbnails.FontFile} {
		_, err := os.Stat(font)
		check(err == nil, "font not found: %s", font)
	}
	return errors.Join(errs...)
}
