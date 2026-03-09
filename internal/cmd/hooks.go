package cmd

import (
	"log/slog"
	"path/filepath"

	"obsync/internal/config"
	"obsync/internal/hooks"
)

// loadHookRunner creates a hooks.Runner by loading both global and vault-local
// hook configurations. Errors are logged as warnings and a no-op runner is
// returned so that hook misconfiguration never blocks sync.
func loadHookRunner(vaultPath, vaultName, vaultID string) *hooks.Runner {
	globalPath, err := config.HooksConfigPath()
	if err != nil {
		slog.Warn("failed to resolve global hooks path", "error", err)
		globalPath = ""
	}

	localPath := filepath.Join(vaultPath, ".obsync-hooks.json")

	cfg, err := hooks.LoadHooks(globalPath, localPath)
	if err != nil {
		slog.Warn("failed to load hooks config", "error", err)
		cfg = nil
	}

	return hooks.NewRunner(cfg, vaultName, vaultID, vaultPath)
}
