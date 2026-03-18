//go:build linux

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"obsync/internal/ui"
)

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

type serviceData struct {
	VaultID         string
	ExecStart       string
	KeyringPassword string
}

func serviceName(vaultID string) string {
	return fmt.Sprintf("obsync@%s.service", vaultID)
}

func serviceDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func servicePath(vaultID string) (string, error) {
	dir, err := serviceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, serviceName(vaultID)), nil
}

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

var runSystemctl = func(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func platformInstall(u *ui.UI, vaultID, vaultPath, binPath string) error {
	content, err := generateServiceFile(vaultID, vaultPath, binPath)
	if err != nil {
		return err
	}
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
