// Package config loads ~/.autoskills/config.json with environment-variable overrides.
// The LLM is always an OpenAI-compatible endpoint (PRD §10.4): Anthropic, OpenAI, a corporate
// gateway, or local Ollama all satisfy the same three fields.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	// LLM endpoint base, e.g. "https://api.openai.com/v1", "https://api.anthropic.com/v1",
	// "http://localhost:11434/v1" (Ollama).
	Endpoint string `json:"endpoint"`
	APIKey   string `json:"api_key"`
	Model    string `json:"model"`

	// MaxSuggestionsPerScan caps a single scan's output (suggestion-fatigue guard, PRD §6.2).
	MaxSuggestionsPerScan int `json:"max_suggestions_per_scan"`
	// MinConfidence drops low-confidence suggestions before they are stored.
	MinConfidence float64 `json:"min_confidence"`
	// IgnoreProjects lists project names or repo roots that must never be scanned.
	IgnoreProjects []string `json:"ignore_projects"`
	// TriggerPhrase tunes automation down from "everything" to "on demand": when set, only
	// sessions where the USER typed this phrase (case-insensitive) are distilled —
	// e.g. "autoskills this". Empty means distill every eligible session.
	TriggerPhrase string `json:"trigger_phrase"`
	// AutoAcceptThreshold (0 disables): suggestions at or above this confidence are accepted
	// and written automatically at scan time. Every auto-accept is reversible via
	// `autoskills undo <id>`. The other end of the tuning dial from TriggerPhrase.
	AutoAcceptThreshold float64 `json:"auto_accept_threshold"`
	// SectionBudgetBytes caps the managed AGENTS.md section (anti-"Markdown poisoning":
	// context is a finite resource). On overflow the lowest-confidence skills are demoted to
	// on-demand skill files. Default 12000 (~3k tokens). Codex hard-caps AGENTS.md at 32KiB.
	SectionBudgetBytes int `json:"section_budget_bytes"`
	// DaemonIntervalMinutes is the periodic sweep interval for `autoskills daemon` (file
	// watching triggers scans sooner; this is the safety net). Default 30.
	DaemonIntervalMinutes int `json:"daemon_interval_minutes"`
}

func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".autoskills"
	}
	return filepath.Join(home, ".autoskills")
}

func Path() string { return filepath.Join(Dir(), "config.json") }

func defaults() Config {
	return Config{
		Endpoint:              "https://api.anthropic.com/v1",
		Model:                 "claude-sonnet-4-5",
		MaxSuggestionsPerScan: 10,
		MinConfidence:         0.5,
	}
}

// Load reads the config file if present, then applies env overrides:
// AUTOSKILLS_ENDPOINT, AUTOSKILLS_API_KEY, AUTOSKILLS_MODEL.
// Falls back to ANTHROPIC_API_KEY / OPENAI_API_KEY when no key is configured
// (matching the endpoint's provider when recognizable).
func Load() (Config, error) {
	cfg := defaults()
	raw, err := os.ReadFile(Path())
	switch {
	case err == nil:
		if jerr := json.Unmarshal(raw, &cfg); jerr != nil {
			return cfg, fmt.Errorf("parse %s: %w", Path(), jerr)
		}
	case errors.Is(err, os.ErrNotExist):
		// fine — defaults + env
	default:
		return cfg, err
	}

	if v := os.Getenv("AUTOSKILLS_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv("AUTOSKILLS_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("AUTOSKILLS_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if cfg.APIKey == "" {
		// match the provider key to the endpoint; never send one provider's key to another
		anthropicEndpoint := strings.Contains(cfg.Endpoint, "anthropic")
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" && anthropicEndpoint {
			cfg.APIKey = v
		} else if v := os.Getenv("OPENAI_API_KEY"); v != "" && !anthropicEndpoint {
			cfg.APIKey = v
		}
	}
	if cfg.MaxSuggestionsPerScan <= 0 {
		cfg.MaxSuggestionsPerScan = 10
	}
	if cfg.SectionBudgetBytes <= 0 {
		cfg.SectionBudgetBytes = 12000
	}
	if cfg.DaemonIntervalMinutes <= 0 {
		cfg.DaemonIntervalMinutes = 30
	}
	return cfg, nil
}

