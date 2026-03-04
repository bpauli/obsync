package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadState_MissingFile(t *testing.T) {
	dir := t.TempDir()

	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.VaultUID != "" {
		t.Errorf("expected empty VaultUID, got %q", s.VaultUID)
	}
	if s.Version != 0 {
		t.Errorf("expected version 0, got %d", s.Version)
	}
	if s.Files == nil {
		t.Fatal("expected initialized Files map, got nil")
	}
	if len(s.Files) != 0 {
		t.Errorf("expected empty Files map, got %d entries", len(s.Files))
	}
}

func TestLoadState_ValidFile(t *testing.T) {
	dir := t.TempDir()

	state := State{
		VaultUID: "vault123",
		Version:  42,
		Files: map[string]FileState{
			"notes/foo.md": {
				Hash:     "abc123",
				SyncHash: "def456",
				MTime:    1709553600000,
				CTime:    1709553600000,
				Size:     1234,
			},
		},
	}

	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.VaultUID != "vault123" {
		t.Errorf("expected VaultUID %q, got %q", "vault123", s.VaultUID)
	}
	if s.Version != 42 {
		t.Errorf("expected version 42, got %d", s.Version)
	}
	fs, ok := s.Files["notes/foo.md"]
	if !ok {
		t.Fatal("expected file entry for notes/foo.md")
	}
	if fs.Hash != "abc123" {
		t.Errorf("expected hash %q, got %q", "abc123", fs.Hash)
	}
	if fs.Size != 1234 {
		t.Errorf("expected size 1234, got %d", fs.Size)
	}
}

func TestLoadState_NilFilesMap(t *testing.T) {
	dir := t.TempDir()

	// Write state with null files field
	data := []byte(`{"vault_uid":"v1","version":1,"files":null}`)
	if err := os.WriteFile(filepath.Join(dir, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Files == nil {
		t.Fatal("expected initialized Files map, got nil")
	}
}

func TestLoadState_InvalidJSON(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{bad json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSave_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	s := State{
		VaultUID: "vault-abc",
		Version:  100,
		Files: map[string]FileState{
			"test.md": {Hash: "h1", SyncHash: "s1", MTime: 1000, CTime: 2000, Size: 500},
		},
	}

	if err := s.Save(dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := StatePath(dir)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected permissions 0600, got %o", info.Mode().Perm())
	}
}

func TestSave_AtomicWrite(t *testing.T) {
	dir := t.TempDir()

	s := State{VaultUID: "v1", Version: 1, Files: make(map[string]FileState)}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	// Verify no tmp file remains
	tmp := StatePath(dir) + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after save")
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	original := State{
		VaultUID: "round-trip-vault",
		Version:  9999,
		Files: map[string]FileState{
			"a.md":        {Hash: "h1", SyncHash: "s1", MTime: 100, CTime: 200, Size: 10},
			"sub/b.md":    {Hash: "h2", SyncHash: "s2", MTime: 300, CTime: 400, Size: 20},
			"deep/c/d.md": {Hash: "h3", SyncHash: "s3", MTime: 500, CTime: 600, Size: 30},
		},
	}

	if err := original.Save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.VaultUID != original.VaultUID {
		t.Errorf("VaultUID: got %q, want %q", loaded.VaultUID, original.VaultUID)
	}
	if loaded.Version != original.Version {
		t.Errorf("Version: got %d, want %d", loaded.Version, original.Version)
	}
	if len(loaded.Files) != len(original.Files) {
		t.Fatalf("Files count: got %d, want %d", len(loaded.Files), len(original.Files))
	}

	for path, origFS := range original.Files {
		loadedFS, ok := loaded.Files[path]
		if !ok {
			t.Errorf("missing file entry %q", path)
			continue
		}
		if loadedFS != origFS {
			t.Errorf("file %q: got %+v, want %+v", path, loadedFS, origFS)
		}
	}
}

func TestStatePath(t *testing.T) {
	got := StatePath("/home/user/vault")
	want := filepath.Join("/home/user/vault", ".obsync-state.json")
	if got != want {
		t.Errorf("StatePath: got %q, want %q", got, want)
	}
}

func TestSave_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()

	s1 := State{VaultUID: "v1", Version: 1, Files: map[string]FileState{
		"old.md": {Hash: "old", SyncHash: "old", MTime: 1, CTime: 1, Size: 1},
	}}
	if err := s1.Save(dir); err != nil {
		t.Fatal(err)
	}

	s2 := State{VaultUID: "v1", Version: 2, Files: map[string]FileState{
		"new.md": {Hash: "new", SyncHash: "new", MTime: 2, CTime: 2, Size: 2},
	}}
	if err := s2.Save(dir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 2 {
		t.Errorf("expected version 2, got %d", loaded.Version)
	}
	if _, ok := loaded.Files["old.md"]; ok {
		t.Error("old.md should not exist after overwrite")
	}
	if _, ok := loaded.Files["new.md"]; !ok {
		t.Error("new.md should exist after overwrite")
	}
}
