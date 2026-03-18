package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"obsync/internal/config"
	"obsync/internal/secrets"
	"obsync/internal/ui"
)

// InstallCmd installs a background service for continuous vault sync.
type InstallCmd struct {
	Vault string `arg:"" help:"Vault name or ID to sync."`
	Path  string `arg:"" help:"Local directory path for the vault." type:"path"`
}

// executablePath is a var for testability.
var executablePath = os.Executable

func (c *InstallCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)
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
	_, err = store.GetToken(cfg.Email)
	if err != nil {
		return &ExitError{Code: ExitAuth, Err: fmt.Errorf("no token found — run 'obsync login' first: %w", err)}
	}
	vaultID := c.Vault
	binPath, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	return platformInstall(u, vaultID, c.Path, binPath)
}
