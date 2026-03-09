package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/crypto"
	"obsync/internal/hooks"
	"obsync/internal/secrets"
	"obsync/internal/sync"
	"obsync/internal/ui"
)

// PullCmd downloads remote changes to a local directory.
type PullCmd struct {
	Vault        string `arg:"" help:"Vault name or ID to pull from."`
	Path         string `arg:"" help:"Local directory path for the vault." type:"path"`
	Password     string `help:"E2E encryption password." short:"p"`
	SavePassword bool   `help:"Save E2E password to keyring for future use." short:"s"`
}

// syncConnectFunc is the function used to connect to the sync server.
// Package-level var for test injection.
var syncConnectFunc = sync.Connect

// promptE2EPassword reads an E2E encryption password from the terminal.
// Package-level var for test injection.
var promptE2EPassword = func(u *ui.UI) (string, error) {
	u.Err().Print("E2E password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	pw := string(b)
	if pw == "" {
		return "", errors.New("password cannot be empty")
	}
	return pw, nil
}

func (c *PullCmd) Run(ctx context.Context, flags *RootFlags) error {
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
	keyHash, err := computeKeyHash(key, vault.Salt, vault.EncryptionVersion)
	if err != nil {
		return fmt.Errorf("compute key hash: %w", err)
	}

	// Save password if requested.
	if c.SavePassword {
		if err := store.SetE2EPassword(vault.ID, e2ePassword); err != nil {
			return fmt.Errorf("save e2e password: %w", err)
		}
	}

	// Ensure local path exists.
	if err := os.MkdirAll(c.Path, 0o755); err != nil {
		return fmt.Errorf("create vault dir: %w", err)
	}

	// Load sync state.
	state, err := sync.LoadState(c.Path)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	state.VaultUID = vault.ID

	// Load hooks.
	hookRunner := loadHookRunner(c.Path, vault.Name, vault.ID)

	// Fire PrePull hook.
	if err := hookRunner.Fire(ctx, hooks.Event{Event: hooks.PrePull}); err != nil {
		return fmt.Errorf("pre-pull hook: %w", err)
	}

	// Get WebSocket host.
	host, err := client.VaultAccess(ctx, token, vault.ID, keyHash, vault.Host, vault.EncryptionVersion)
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

	initial := state.Version == 0
	slog.Debug("connecting to sync server", "host", host, "version", state.Version, "initial", initial)

	sc, err := syncConnectFunc(ctx, sync.ConnectParams{
		Host:              host,
		Token:             token,
		VaultUID:          vault.ID,
		KeyHash:           keyHash,
		Version:           state.Version,
		Initial:           initial,
		Device:            device,
		EncryptionVersion: vault.EncryptionVersion,
		Key:               key,
	})
	if err != nil {
		return fmt.Errorf("connect sync: %w", err)
	}
	defer sc.Close()

	// Phase 1: Collect all push notifications until ready.
	var pushes []*sync.PushMessage
	var deleteCount int
	for {
		msg, err := sc.ReceivePush(ctx)
		if err != nil {
			return fmt.Errorf("receive push: %w", err)
		}

		if msg.Op == "ready" {
			state.Version = msg.UID // UID holds the version for ready messages
			break
		}

		if msg.Op != "push" {
			continue
		}

		pushes = append(pushes, msg)
	}

	// Phase 2: Process collected pushes — pull file content after ready.
	var fileCount int
	for _, msg := range pushes {
		plainPath, err := crypto.DecodePath(key, msg.Path, vault.EncryptionVersion)
		if err != nil {
			return fmt.Errorf("decrypt path: %w", err)
		}

		if msg.Deleted {
			localPath := filepath.Join(c.Path, plainPath)
			if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
				slog.Warn("failed to delete file", "path", plainPath, "error", err)
			}
			delete(state.Files, plainPath)
			deleteCount++
			if err := hookRunner.Fire(ctx, hooks.Event{
				Event: hooks.PostFileDeleted,
				File:  &hooks.FileInfo{Path: plainPath, LocalPath: localPath},
			}); err != nil {
				return fmt.Errorf("post-file-deleted hook: %w", err)
			}
			continue
		}

		if msg.Folder {
			dirPath := filepath.Join(c.Path, plainPath)
			if err := os.MkdirAll(dirPath, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", plainPath, err)
			}
			continue
		}

		// Pull file content.
		content, err := sc.PullFile(ctx, msg.UID)
		if errors.Is(err, sync.ErrFileDeleted) {
			slog.Debug("file deleted on server during pull", "path", plainPath)
			continue
		}
		if err != nil {
			return fmt.Errorf("pull file %s: %w", plainPath, err)
		}

		// Write file to disk.
		localPath := filepath.Join(c.Path, plainPath)
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return fmt.Errorf("create parent dir: %w", err)
		}
		if err := os.WriteFile(localPath, content, 0o644); err != nil {
			return fmt.Errorf("write file %s: %w", plainPath, err)
		}

		if msg.MTime > 0 {
			mtime := time.UnixMilli(msg.MTime)
			_ = os.Chtimes(localPath, mtime, mtime)
		}

		plainHash := ""
		if msg.Hash != "" {
			plainHash, err = crypto.DecodePath(key, msg.Hash, vault.EncryptionVersion)
			if err != nil {
				slog.Warn("failed to decrypt hash", "path", plainPath, "error", err)
			}
		}

		state.Files[plainPath] = sync.FileState{
			Hash:     plainHash,
			SyncHash: plainHash,
			MTime:    msg.MTime,
			CTime:    msg.CTime,
			Size:     msg.Size,
		}
		fileCount++
		slog.Debug("pulled", "path", plainPath, "size", msg.Size)

		if err := hookRunner.Fire(ctx, hooks.Event{
			Event: hooks.PostFileReceived,
			File: &hooks.FileInfo{
				Path:      plainPath,
				LocalPath: localPath,
				Size:      msg.Size,
				Hash:      plainHash,
			},
		}); err != nil {
			return fmt.Errorf("post-file-received hook: %w", err)
		}
	}

	// Save updated state.
	if err := state.Save(c.Path); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	// Fire PostPull hook.
	if err := hookRunner.Fire(ctx, hooks.Event{
		Event: hooks.PostPull,
		Stats: &hooks.OperationStats{FilesSynced: fileCount, FilesDeleted: deleteCount},
	}); err != nil {
		return fmt.Errorf("post-pull hook: %w", err)
	}

	u.Out().Successf("Pull complete: %d files synced, %d deleted (version %d)", fileCount, deleteCount, state.Version)
	return nil
}

// computeKeyHash returns the appropriate keyhash for the vault's encryption version.
func computeKeyHash(key []byte, salt string, encVer int) (string, error) {
	switch encVer {
	case 0:
		return crypto.KeyHash(key), nil
	case 2, 3:
		return crypto.KeyHashV2(key, salt)
	default:
		return "", fmt.Errorf("unsupported encryption version: %d", encVer)
	}
}

// resolveVault finds a vault by name or ID in the list response.
func resolveVault(resp *api.ListVaultsResponse, nameOrID string) (api.Vault, error) {
	all := append(resp.Vaults, resp.Shared...)
	for _, v := range all {
		if v.ID == nameOrID || v.Name == nameOrID {
			return v, nil
		}
	}
	return api.Vault{}, fmt.Errorf("vault %q not found", nameOrID)
}

// resolveE2EPassword determines the E2E password from flags, keyring, or prompt.
func (c *PullCmd) resolveE2EPassword(u *ui.UI, store secrets.Store, vault api.Vault) (string, error) {
	// Non-E2E vault: use vault's password field as the password.
	if vault.Password != "" && vault.Salt != "" {
		// If the vault has a password field, it's used as the "salt" for key derivation
		// but the actual password comes from the user or keyring.
	}

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
