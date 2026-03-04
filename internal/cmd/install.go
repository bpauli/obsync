package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"obsync/internal/config"
	"obsync/internal/secrets"
	"obsync/internal/ui"
)

// InstallCmd installs a systemd user service for continuous vault sync.
type InstallCmd struct {
	Vault string `arg:"" help:"Vault name or ID to sync."`
	Path  string `arg:"" help:"Local directory path for the vault." type:"path"`
}

// serviceTemplate is the systemd unit file template.
var serviceTemplate = template.Must(template.New("service").Parse(`[Unit]
Description=obsync — Obsidian Sync for vault {{ .VaultID }}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{ .ExecStart }}
Restart=on-failure
RestartSec=5
Environment=OBSYNC_KEYRING_BACKEND=file
{{ if .KeyringPassword -}}
Environment=OBSYNC_KEYRING_PASSWORD={{ .KeyringPassword }}
{{ end -}}

[Install]
WantedBy=default.target
`))

// serviceData holds template variables for the systemd unit file.
type serviceData struct {
	VaultID         string
	ExecStart       string
	KeyringPassword string
}

// serviceName returns the systemd service name for a vault.
func serviceName(vaultID string) string {
	return fmt.Sprintf("obsync@%s.service", vaultID)
}

// serviceDir returns the systemd user service directory.
func serviceDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// servicePath returns the full path to the service file for a vault.
func servicePath(vaultID string) (string, error) {
	dir, err := serviceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, serviceName(vaultID)), nil
}

// generateServiceFile generates the systemd unit file content.
func generateServiceFile(vaultID, vaultPath, obsyncBin string) (string, error) {
	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return "", fmt.Errorf("resolve vault path: %w", err)
	}

	execStart := fmt.Sprintf("%s watch %s %s", obsyncBin, vaultID, absPath)

	data := serviceData{
		VaultID:         vaultID,
		ExecStart:       execStart,
		KeyringPassword: os.Getenv("OBSYNC_KEYRING_PASSWORD"),
	}

	var buf strings.Builder
	if err := serviceTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render service template: %w", err)
	}
	return buf.String(), nil
}

// executablePath is a var for testability.
var executablePath = os.Executable

// runSystemctl is a var for testability.
var runSystemctl = func(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *InstallCmd) Run(ctx context.Context, flags *RootFlags) error {
	u := ui.FromContext(ctx)

	// Load config + token to verify login.
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

	// Resolve vault ID — use the argument directly (could be name or ID).
	vaultID := c.Vault

	// Get the obsync binary path.
	binPath, err := executablePath()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	// Generate service file.
	content, err := generateServiceFile(vaultID, c.Path, binPath)
	if err != nil {
		return err
	}

	// Write service file.
	svcPath, err := servicePath(vaultID)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(svcPath), 0o755); err != nil {
		return fmt.Errorf("create systemd dir: %w", err)
	}

	if err := os.WriteFile(svcPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}

	u.Out().Successf("Service file written: %s", svcPath)

	// Reload, enable, and start.
	if err := runSystemctl("daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}

	svcName := serviceName(vaultID)
	if err := runSystemctl("enable", svcName); err != nil {
		return fmt.Errorf("enable service: %w", err)
	}

	if err := runSystemctl("start", svcName); err != nil {
		return fmt.Errorf("start service: %w", err)
	}

	u.Out().Successf("Service %s enabled and started", svcName)
	u.Out().Infof("Tip: run 'loginctl enable-linger %s' for headless servers to persist services after logout", os.Getenv("USER"))

	return nil
}
