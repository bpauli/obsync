package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
)

// HooksConfig is the top-level configuration for hooks.
type HooksConfig struct {
	Hooks map[EventType][]MatcherGroup `json:"hooks"`
}

// MatcherGroup ties a file-path matcher to a list of hook handlers.
type MatcherGroup struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []HookHandler `json:"hooks"`

	compiled *regexp.Regexp
}

// HookHandler defines a single hook command to execute.
//
// Exit code convention:
//   - 0: success, continue
//   - 2: block the current operation (stderr is used as the error reason)
//   - any other: non-blocking failure, log warning and continue
type HookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// Matches reports whether the given file path matches this group's matcher.
// An empty matcher, "*", or missing matcher matches everything.
func (mg *MatcherGroup) Matches(path string) bool {
	if mg.Matcher == "" || mg.Matcher == "*" {
		return true
	}
	if mg.compiled == nil {
		re, err := regexp.Compile(mg.Matcher)
		if err != nil {
			return false
		}
		mg.compiled = re
	}
	return mg.compiled.MatchString(path)
}

// LoadHooks loads and merges hook configurations from the global and vault-local paths.
// Missing files are silently ignored. Both files contribute hooks additively.
func LoadHooks(globalPath, vaultLocalPath string) (*HooksConfig, error) {
	global, err := loadFile(globalPath)
	if err != nil {
		return nil, fmt.Errorf("load global hooks %s: %w", globalPath, err)
	}

	local, err := loadFile(vaultLocalPath)
	if err != nil {
		return nil, fmt.Errorf("load vault hooks %s: %w", vaultLocalPath, err)
	}

	return mergeConfigs(global, local), nil
}

func loadFile(path string) (*HooksConfig, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var cfg HooksConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

func mergeConfigs(a, b *HooksConfig) *HooksConfig {
	if a == nil && b == nil {
		return &HooksConfig{Hooks: make(map[EventType][]MatcherGroup)}
	}
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}

	merged := &HooksConfig{Hooks: make(map[EventType][]MatcherGroup)}
	for event, groups := range a.Hooks {
		merged.Hooks[event] = append(merged.Hooks[event], groups...)
	}
	for event, groups := range b.Hooks {
		merged.Hooks[event] = append(merged.Hooks[event], groups...)
	}
	return merged
}
