package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"obsync/internal/api"
	"obsync/internal/config"
	"obsync/internal/crypto"
	"obsync/internal/hooks"
	"obsync/internal/secrets"
	"obsync/internal/sync"
	"obsync/internal/ui"
)

// WatchCmd runs continuous bidirectional sync.
type WatchCmd struct {
	Vault        string `arg:"" help:"Vault name or ID to watch."`
	Path         string `arg:"" help:"Local directory path for the vault." type:"path"`
	Password     string `help:"E2E encryption password." short:"p"`
	SavePassword bool   `help:"Save E2E password to keyring for future use." short:"s"`

	encVer     int           // set during Run from vault.EncryptionVersion
	hookRunner *hooks.Runner // set during Run
}

// debounceDuration is the time to wait after a filesystem event before pushing.
// Package-level var for test injection.
var debounceDuration = 500 * time.Millisecond

func (c *WatchCmd) Run(ctx context.Context, flags *RootFlags) error {
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

	// Resolve vault.
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

	device := cfg.Device
	if device == "" {
		device, _ = os.Hostname()
	}

	// Set up signal handling for graceful shutdown.
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Load hooks and connect.
	c.encVer = vault.EncryptionVersion
	c.hookRunner = loadHookRunner(c.Path, vault.Name, vault.ID)
	return c.syncLoop(ctx, u, client, token, vault, key, keyHash, device, &state)
}

func (c *WatchCmd) syncLoop(ctx context.Context, u *ui.UI, client *api.Client,
	token string, vault api.Vault, key []byte, keyHash string,
	device string, state *sync.State) error {

	backoff := time.Second
	maxBackoff := 60 * time.Second
	hadFailure := false

	for {
		if hadFailure {
			_ = c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.ConnectionRestored})
		}

		err := c.runSession(ctx, u, client, token, vault, key, keyHash, device, state)
		if err == nil || ctx.Err() != nil {
			if ctx.Err() != nil {
				_ = c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.WatchStop})
				u.Out().Infof("Watch stopped")
			}
			return nil
		}

		slog.Warn("sync session ended", "error", err)
		u.Err().Errorf("Connection lost: %v — reconnecting in %s", err, backoff)

		_ = c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.ConnectionLost, Error: err.Error()})
		hadFailure = true

		select {
		case <-ctx.Done():
			_ = c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.WatchStop})
			return nil
		case <-time.After(backoff):
		}

		// Exponential backoff.
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *WatchCmd) runSession(ctx context.Context, u *ui.UI, client *api.Client,
	token string, vault api.Vault, key []byte, keyHash string,
	device string, state *sync.State) error {

	// Get WebSocket host.
	host, err := client.VaultAccess(ctx, token, vault.ID, keyHash, vault.Host, vault.EncryptionVersion)
	if err != nil {
		return fmt.Errorf("vault access: %w", err)
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

	// Initial pull: receive pushes until ready.
	pullCount, deleteCount, err := c.receiveUntilReady(ctx, sc, key, state)
	if err != nil {
		return fmt.Errorf("initial pull: %w", err)
	}

	// Initial push: scan and push local changes.
	pushCount, pushDelCount, err := c.pushLocalChanges(ctx, sc, key, state)
	if err != nil {
		return fmt.Errorf("initial push: %w", err)
	}

	if err := state.Save(c.Path); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	u.Out().Successf("Initial sync: pulled %d, deleted %d, pushed %d, push-deleted %d (version %d)",
		pullCount, deleteCount, pushCount, pushDelCount, state.Version)

	// Fire WatchStart after initial sync completes.
	_ = c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.WatchStart})

	// Start heartbeat.
	sc.StartHeartbeat(ctx)

	// Set up fsnotify watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()

	if err := addWatchDirs(watcher, c.Path); err != nil {
		return fmt.Errorf("add watch dirs: %w", err)
	}

	// Channel for errors from goroutines.
	errCh := make(chan error, 2)

	// Goroutine A: read server pushes.
	go func() {
		errCh <- c.receiveLoop(ctx, sc, key, state, u)
	}()

	// Goroutine B: watch local filesystem changes.
	go func() {
		errCh <- c.watchLoop(ctx, watcher, sc, key, state, u)
	}()

	// Wait for either goroutine to finish or context cancellation.
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (c *WatchCmd) receiveUntilReady(ctx context.Context, sc *sync.Client, key []byte, state *sync.State) (int, int, error) {
	// Phase 1: Collect all push notifications until ready.
	// The server sends all pushes in a burst, then "ready". We cannot pull
	// files inline because the next message would be another push, not the
	// pull response.
	var pushes []*sync.PushMessage
	for {
		msg, err := sc.ReceivePush(ctx)
		if err != nil {
			return 0, 0, err
		}

		if msg.Op == "ready" {
			state.Version = msg.UID
			break
		}

		if msg.Op == "push" {
			pushes = append(pushes, msg)
		}
	}

	// Phase 2: Process collected pushes — pull file content after ready.
	var fileCount, deleteCount int
	for _, msg := range pushes {
		fc, dc, err := c.handlePush(ctx, sc, key, c.encVer, state, msg)
		if err != nil {
			return fileCount, deleteCount, err
		}
		fileCount += fc
		deleteCount += dc
	}
	return fileCount, deleteCount, nil
}

func (c *WatchCmd) handlePush(ctx context.Context, sc *sync.Client, key []byte, encVer int, state *sync.State, msg *sync.PushMessage) (int, int, error) {
	plainPath, err := crypto.DecodePath(key, msg.Path, encVer)
	if err != nil {
		return 0, 0, fmt.Errorf("decrypt path: %w", err)
	}

	if msg.Deleted {
		localPath := filepath.Join(c.Path, plainPath)
		if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to delete file", "path", plainPath, "error", err)
		}
		delete(state.Files, plainPath)
		slog.Debug("deleted", "path", plainPath)
		if err := c.hookRunner.Fire(ctx, hooks.Event{
			Event: hooks.PostFileDeleted,
			File:  &hooks.FileInfo{Path: plainPath, LocalPath: localPath},
		}); err != nil {
			return 0, 1, fmt.Errorf("post-file-deleted hook: %w", err)
		}
		return 0, 1, nil
	}

	if msg.Folder {
		dirPath := filepath.Join(c.Path, plainPath)
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			return 0, 0, fmt.Errorf("create dir %s: %w", plainPath, err)
		}
		return 0, 0, nil
	}

	content, err := sc.PullFile(ctx, msg.UID)
	if errors.Is(err, sync.ErrFileDeleted) {
		slog.Debug("file deleted on server during pull", "path", plainPath)
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("pull file %s: %w", plainPath, err)
	}

	localPath := filepath.Join(c.Path, plainPath)
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return 0, 0, fmt.Errorf("create parent dir: %w", err)
	}
	if err := os.WriteFile(localPath, content, 0o644); err != nil {
		return 0, 0, fmt.Errorf("write file %s: %w", plainPath, err)
	}

	if msg.MTime > 0 {
		mtime := time.UnixMilli(msg.MTime)
		_ = os.Chtimes(localPath, mtime, mtime)
	}

	plainHash := ""
	if msg.Hash != "" {
		plainHash, err = crypto.DecodePath(key, msg.Hash, encVer)
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
	slog.Debug("pulled", "path", plainPath, "size", msg.Size)
	if err := c.hookRunner.Fire(ctx, hooks.Event{
		Event: hooks.PostFileReceived,
		File: &hooks.FileInfo{
			Path:      plainPath,
			LocalPath: localPath,
			Size:      msg.Size,
			Hash:      plainHash,
		},
	}); err != nil {
		return 1, 0, fmt.Errorf("post-file-received hook: %w", err)
	}
	return 1, 0, nil
}

func (c *WatchCmd) pushLocalChanges(ctx context.Context, sc *sync.Client, key []byte, state *sync.State) (int, int, error) {
	localFiles, err := scanLocalFiles(c.Path)
	if err != nil {
		return 0, 0, fmt.Errorf("scan local files: %w", err)
	}

	var pushCount, deleteCount int

	// Push changed/new files.
	for path, hash := range localFiles {
		if fs, ok := state.Files[path]; ok && fs.SyncHash == hash {
			continue
		}

		localPath := filepath.Join(c.Path, path)
		data, err := os.ReadFile(localPath)
		if err != nil {
			return pushCount, deleteCount, fmt.Errorf("read file %s: %w", path, err)
		}

		info, err := os.Stat(localPath)
		if err != nil {
			return pushCount, deleteCount, fmt.Errorf("stat file %s: %w", path, err)
		}

		now := time.Now().UnixMilli()
		mtime := info.ModTime().UnixMilli()

		if err := sc.PushFile(ctx, path, data, hash, info.Size(), now, mtime, false); err != nil {
			if errors.Is(err, sync.ErrFileTooLarge) {
				slog.Warn("skipping file (exceeds server size limit)", "path", path, "size", info.Size())
				continue
			}
			return pushCount, deleteCount, fmt.Errorf("push file %s: %w", path, err)
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

		if err := c.hookRunner.Fire(ctx, hooks.Event{
			Event: hooks.PostFilePushed,
			File: &hooks.FileInfo{
				Path:      path,
				LocalPath: localPath,
				Size:      info.Size(),
				Hash:      hash,
			},
		}); err != nil {
			return pushCount, deleteCount, fmt.Errorf("post-file-pushed hook: %w", err)
		}
	}

	// Push deletions.
	for path := range state.Files {
		if _, ok := localFiles[path]; !ok {
			if err := sc.PushDelete(ctx, path); err != nil {
				return pushCount, deleteCount, fmt.Errorf("push delete %s: %w", path, err)
			}
			delete(state.Files, path)
			deleteCount++
			slog.Debug("pushed delete", "path", path)

			if err := c.hookRunner.Fire(ctx, hooks.Event{
				Event: hooks.PostFileDeleted,
				File:  &hooks.FileInfo{Path: path, LocalPath: filepath.Join(c.Path, path)},
			}); err != nil {
				return pushCount, deleteCount, fmt.Errorf("post-file-deleted hook: %w", err)
			}
		}
	}

	return pushCount, deleteCount, nil
}

func (c *WatchCmd) receiveLoop(ctx context.Context, sc *sync.Client, key []byte, state *sync.State, u *ui.UI) error {
	for {
		msg, err := sc.ReceivePush(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("receive push: %w", err)
		}

		if msg.Op == "ready" {
			state.Version = msg.UID
			if err := state.Save(c.Path); err != nil {
				slog.Warn("failed to save state", "error", err)
			}
			continue
		}

		if msg.Op != "push" {
			continue
		}

		fc, dc, err := c.handlePush(ctx, sc, key, c.encVer, state, msg)
		if err != nil {
			return err
		}

		if err := state.Save(c.Path); err != nil {
			slog.Warn("failed to save state", "error", err)
		}

		if fc > 0 {
			plainPath, _ := crypto.DecodePath(key, msg.Path, c.encVer)
			u.Out().Infof("Received: %s", plainPath)
		}
		if dc > 0 {
			plainPath, _ := crypto.DecodePath(key, msg.Path, c.encVer)
			u.Out().Infof("Deleted: %s", plainPath)
		}

		if fc > 0 || dc > 0 {
			if err := c.hookRunner.Fire(ctx, hooks.Event{
				Event: hooks.PostPull,
				Stats: &hooks.OperationStats{FilesSynced: fc, FilesDeleted: dc},
			}); err != nil {
				slog.Warn("post-pull hook failed", "error", err)
			}
		}
	}
}

func (c *WatchCmd) watchLoop(ctx context.Context, watcher *fsnotify.Watcher, sc *sync.Client, key []byte, state *sync.State, u *ui.UI) error {
	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			debounce.Stop()
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Skip hidden files and state file.
			name := filepath.Base(event.Name)
			if strings.HasPrefix(name, ".") {
				continue
			}

			// If a new directory was created, add it to the watcher.
			if event.Has(fsnotify.Create) {
				info, err := os.Stat(event.Name)
				if err == nil && info.IsDir() {
					_ = addWatchDirs(watcher, event.Name)
				}
			}

			// Debounce: reset timer on each event.
			if !debounce.Stop() {
				select {
				case <-debounce.C:
				default:
				}
			}
			debounce.Reset(debounceDuration)
			pending = true

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("fsnotify error", "error", err)
			_ = c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.SyncError, Error: err.Error()})

		case <-debounce.C:
			if !pending {
				continue
			}
			pending = false

			if err := c.hookRunner.Fire(ctx, hooks.Event{Event: hooks.PrePush}); err != nil {
				var blocked *hooks.BlockedError
				if errors.As(err, &blocked) {
					slog.Warn("pre-push hook blocked push", "error", err)
					continue
				}
			}

			pushCount, delCount, err := c.pushLocalChanges(ctx, sc, key, state)
			if err != nil {
				return fmt.Errorf("push local changes: %w", err)
			}

			if pushCount > 0 || delCount > 0 {
				if err := state.Save(c.Path); err != nil {
					slog.Warn("failed to save state", "error", err)
				}
				u.Out().Infof("Pushed %d files, deleted %d", pushCount, delCount)

				if err := c.hookRunner.Fire(ctx, hooks.Event{
					Event: hooks.PostPush,
					Stats: &hooks.OperationStats{FilesSynced: pushCount, FilesDeleted: delCount},
				}); err != nil {
					slog.Warn("post-push hook failed", "error", err)
				}
			}
		}
	}
}

// addWatchDirs recursively adds a directory and all its subdirectories to the watcher.
// It skips hidden directories except .obsidian/ (vault config).
func addWatchDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible directories
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && strings.HasPrefix(name, ".") && name != ".obsidian" {
				return filepath.SkipDir
			}
			return watcher.Add(path)
		}
		return nil
	})
}

// resolveE2EPassword determines the E2E password from flags, keyring, or prompt.
func (c *WatchCmd) resolveE2EPassword(u *ui.UI, store secrets.Store, vault api.Vault) (string, error) {
	if c.Password != "" {
		return c.Password, nil
	}

	pw, err := store.GetE2EPassword(vault.ID)
	if err == nil && pw != "" {
		return pw, nil
	}

	pw, err = promptE2EPassword(u)
	if err != nil {
		return "", &ExitError{Code: ExitAuth, Err: fmt.Errorf("e2e password: %w", err)}
	}
	return pw, nil
}

// fileHash computes the SHA-256 hex hash of data.
func fileHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
