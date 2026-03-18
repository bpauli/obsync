//go:build darwin

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

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.BinPath}}</string>
		<string>watch</string>
		<string>{{.VaultID}}</string>
		<string>{{.VaultPath}}</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>{{.LogDir}}/{{.VaultID}}.out.log</string>
	<key>StandardErrorPath</key>
	<string>{{.LogDir}}/{{.VaultID}}.err.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
	</dict>
</dict>
</plist>
`))

type plistData struct {
	Label     string
	BinPath   string
	VaultID   string
	VaultPath string
	LogDir    string
}

func plistLabel(vaultID string) string {
	return fmt.Sprintf("com.obsync.%s", vaultID)
}

func plistPath(vaultID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel(vaultID)+".plist"), nil
}

func logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Logs", "obsync"), nil
}

func generatePlistFile(vaultID, vaultPath, binPath string) (string, error) {
	absPath, err := filepath.Abs(vaultPath)
	if err != nil {
		return "", fmt.Errorf("resolve vault path: %w", err)
	}
	logd, err := logDir()
	if err != nil {
		return "", err
	}
	data := plistData{
		Label:     plistLabel(vaultID),
		BinPath:   binPath,
		VaultID:   vaultID,
		VaultPath: absPath,
		LogDir:    logd,
	}
	var buf strings.Builder
	if err := plistTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render plist template: %w", err)
	}
	return buf.String(), nil
}

var runLaunchctl = func(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func platformInstall(u *ui.UI, vaultID, vaultPath, binPath string) error {
	content, err := generatePlistFile(vaultID, vaultPath, binPath)
	if err != nil {
		return err
	}
	pPath, err := plistPath(vaultID)
	if err != nil {
		return err
	}
	logd, err := logDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logd, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pPath), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	label := plistLabel(vaultID)
	uid := fmt.Sprintf("gui/%d", os.Getuid())
	_ = runLaunchctl("bootout", uid+"/"+label)
	if err := os.WriteFile(pPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write plist file: %w", err)
	}
	u.Out().Successf("Plist written: %s", pPath)
	if err := runLaunchctl("bootstrap", uid, pPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w", err)
	}
	u.Out().Successf("Service %s loaded and started", label)
	u.Out().Infof("Logs: ~/Library/Logs/obsync/%s.{out,err}.log", vaultID)
	return nil
}
