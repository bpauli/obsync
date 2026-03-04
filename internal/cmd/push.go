package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/crypto"
	"obsync/internal/secrets"
	"obsync/internal/sync"
	"obsync/internal/ui"
)

// PushCmd uploads local changes to a remote vault.
type PushCmd struct {
	Vault        string `arg:"" help:"Vault name or ID to push to."`
	Path         string `arg:"" help:"Local directory path for the vault." type:"path"`
	Password     string `help:"E2E encryption password." short:"p"`
	SavePassword bool   `help:"Save E2E password to keyring for future use." short:"s"`
}

func (c *PushCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)

	// Load config and token.
	cfgPath := flags.Config
	if cfgPath == "" {
		var err error
		cfgPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve config path: %w", err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Email == "" {
		return &ExitError{Code: ExitAuth, Err: errors.New("not logged in — run 'obsync login' first")}
	}

	store, err := secrets.OpenDefault()
	if err != nil {
		return &ExitError{Code: ExitConfig, Err: fmt.Errorf("open keyring: %w", err)}
	}

	token, err := store.GetToken(cfg.Email)
	if err != nil {
		return &ExitError{Code: ExitAuth, Err: fmt.Errorf("no token found — run 'obsync login' first: %w", err)}
	}

	// Resolve vault by name or ID.
	client := newAPIClient()
	vaultsResp, err := client.ListVaults(ctx, token)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return &ExitError{Code: ExitAuth, Err: fmt.Errorf("list vaults: %s", apiErr.Message)}
		}
		return fmt.Errorf("list vaults: %w", err)
	}

	vault, err := resolveVault(vaultsResp, c.Vault)
	if err != nil {
		return &ExitError{Code: ExitNotFound, Err: err}
	}

	// Derive encryption key.
	e2ePassword, err := c.resolveE2EPassword(u, store, vault)
	if err != nil {
		return err
	}

	key, err := crypto.DeriveKey(e2ePassword, vault.Salt)
	if err != nil {
		return fmt.Errorf("derive key: %w", err)
	}
	keyHash := crypto.KeyHash(key)

	// Save password if requested.
	if c.SavePassword {
		if err := store.SetE2EPassword(vault.ID, e2ePassword); err != nil {
			return fmt.Errorf("save e2e password: %w", err)
		}
	}

	// Load sync state.
	state, err := sync.LoadState(c.Path)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	state.VaultUID = vault.ID

	// Scan local files and compute hashes.
	localFiles, err := scanLocalFiles(c.Path)
	if err != nil {
		return fmt.Errorf("scan local files: %w", err)
	}

	// Determine changed/new files and deleted files.
	var pushPaths []string
	for path, hash := range localFiles {
		if fs, ok := state.Files[path]; !ok || fs.SyncHash != hash {
			pushPaths = append(pushPaths, path)
		}
	}

	var deletePaths []string
	for path := range state.Files {
		if _, ok := localFiles[path]; !ok {
			deletePaths = append(deletePaths, path)
		}
	}

	if len(pushPaths) == 0 && len(deletePaths) == 0 {
		u.Out().Infof("Nothing to push — vault is up to date")
		return nil
	}

	// Get WebSocket host.
	host, err := client.VaultAccess(ctx, token, vault.ID, keyHash, 3)
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) {
			return &ExitError{Code: ExitAuth, Err: fmt.Errorf("vault access: %s", apiErr.Message)}
		}
		return fmt.Errorf("vault access: %w", err)
	}

	// Connect WebSocket.
	device := cfg.Device
	if device == "" {
		device, _ = os.Hostname()
	}

	slog.Debug("connecting to sync server", "host", host, "version", state.Version)

	sc, err := syncConnectFunc(ctx, sync.ConnectParams{
		Host:              host,
		Token:             token,
		VaultUID:          vault.ID,
		KeyHash:           keyHash,
		Version:           state.Version,
		Initial:           false,
		Device:            device,
		EncryptionVersion: 3,
		Key:               key,
	})
	if err != nil {
		return fmt.Errorf("connect sync: %w", err)
	}
	defer sc.Close()

	// Push changed/new files.
	var pushCount int
	for _, path := range pushPaths {
		localPath := filepath.Join(c.Path, path)
		data, err := os.ReadFile(localPath)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}

		info, err := os.Stat(localPath)
		if err != nil {
			return fmt.Errorf("stat file %s: %w", path, err)
		}

		hash := localFiles[path]
		now := time.Now().UnixMilli()
		mtime := info.ModTime().UnixMilli()

		if err := sc.PushFile(ctx, path, data, hash, info.Size(), now, mtime, false); err != nil {
			return fmt.Errorf("push file %s: %w", path, err)
		}

		state.Files[path] = sync.FileState{
			Hash:     hash,
			SyncHash: hash,
			MTime:    mtime,
			CTime:    now,
			Size:     info.Size(),
		}
		pushCount++
		slog.Debug("pushed", "path", path, "size", info.Size())
	}

	// Push deletions.
	var deleteCount int
	for _, path := range deletePaths {
		if err := sc.PushDelete(ctx, path); err != nil {
			return fmt.Errorf("push delete %s: %w", path, err)
		}
		delete(state.Files, path)
		deleteCount++
		slog.Debug("deleted", "path", path)
	}

	// Save updated state.
	if err := state.Save(c.Path); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	u.Out().Successf("Push complete: %d files pushed, %d deleted", pushCount, deleteCount)
	return nil
}

// resolveE2EPassword determines the E2E password from flags, keyring, or prompt.
func (c *PushCmd) resolveE2EPassword(u *ui.UI, store secrets.Store, vault api.Vault) (string, error) {
	// 1. --password flag
	if c.Password != "" {
		return c.Password, nil
	}

	// 2. Keyring
	pw, err := store.GetE2EPassword(vault.ID)
	if err == nil && pw != "" {
		return pw, nil
	}

	// 3. Prompt
	pw, err = promptE2EPassword(u)
	if err != nil {
		return "", &ExitError{Code: ExitAuth, Err: fmt.Errorf("e2e password: %w", err)}
	}
	return pw, nil
}

// scanLocalFiles walks the vault directory and returns a map of relative paths to SHA-256 hashes.
// It skips the .obsync-state.json file and hidden files/directories (starting with .).
func scanLocalFiles(root string) (map[string]string, error) {
	files := make(map[string]string)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path.
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		// Skip root directory itself.
		if rel == "." {
			return nil
		}

		// Skip hidden files/directories and state file.
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories (we only track files).
		if d.IsDir() {
			return nil
		}

		// Compute SHA-256 hash.
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}

		h := sha256.Sum256(data)
		files[rel] = hex.EncodeToString(h[:])

		return nil
	})

	return files, err
}
