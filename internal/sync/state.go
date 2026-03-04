package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const stateFileName = ".obsync-state.json"

// FileState holds the sync metadata for a single file.
type FileState struct {
	Hash     string `json:"hash"`
	SyncHash string `json:"sync_hash"`
	MTime    int64  `json:"mtime"`
	CTime    int64  `json:"ctime"`
	Size     int64  `json:"size"`
}

// State holds the overall sync state for a vault.
type State struct {
	VaultUID string               `json:"vault_uid"`
	Version  int64                `json:"version"`
	Files    map[string]FileState `json:"files"`
}

// StatePath returns the path to the state file within a vault directory.
func StatePath(vaultPath string) string {
	return filepath.Join(vaultPath, stateFileName)
}

// LoadState reads the sync state from <vaultPath>/.obsync-state.json.
// If the file does not exist, an empty State is returned with an initialized Files map.
func LoadState(vaultPath string) (State, error) {
	p := StatePath(vaultPath)

	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{Files: make(map[string]FileState)}, nil
		}
		return State{}, fmt.Errorf("read state: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("decode state: %w", err)
	}
	if s.Files == nil {
		s.Files = make(map[string]FileState)
	}
	return s, nil
}

// Save writes the state to <vaultPath>/.obsync-state.json atomically
// (write to tmp file then rename).
func (s *State) Save(vaultPath string) error {
	p := StatePath(vaultPath)

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("ensure state dir: %w", err)
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	b = append(b, '\n')

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}

	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("commit state: %w", err)
	}

	return nil
}
