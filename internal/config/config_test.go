package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExample(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Church.Name == "" || len(cfg.Transcription.Glossary) == 0 {
		t.Errorf("example config is missing the church's details: %+v", cfg.Church)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "config.toml"))
	if err == nil || !strings.Contains(err.Error(), "config.example.toml") {
		t.Errorf("Load of a missing file = %v; want it to point at config.example.toml", err)
	}
}
