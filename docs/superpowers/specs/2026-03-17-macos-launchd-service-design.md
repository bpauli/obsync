# macOS Launch Agent Service Support

**Date:** 2026-03-17
**Status:** Approved

## Overview

Add macOS support to the `obsync install`, `obsync uninstall`, and `obsync status` commands using launchd Launch Agents. The CLI auto-detects the OS at compile time via Go build tags — users run the same commands on both Linux and macOS.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Service type | User Launch Agent (`~/Library/LaunchAgents/`) | Matches systemd `--user` approach, no root required, native Keychain access |
| OS detection | Go build tags (`//go:build darwin`/`linux`) | Compile-time separation, idiomatic Go, no runtime branching |
| Logging | `StandardOutPath`/`StandardErrorPath` to `~/Library/Logs/obsync/` | Standard macOS convention, easy to tail |
| Auto-start | `RunAtLoad: true` | Matches Linux behavior where install enables and starts the service |
| Restart policy | `KeepAlive: { SuccessfulExit: false }` | Equivalent to systemd `Restart=on-failure`; clean exits left alone |
| Keyring | Native macOS Keychain (no env vars needed) | Unlike Linux `file` backend, macOS Keychain works natively for user agents |
| Help text | Platform-neutral ("background service") | Avoids build-tagged `root.go` just for help strings |

## File Structure

### New Files

| File | Build tag | Purpose |
|---|---|---|
| `internal/cmd/install_darwin.go` | `//go:build darwin` | `platformInstall()` — render plist, write to LaunchAgents, `launchctl load -w` |
| `internal/cmd/uninstall_darwin.go` | `//go:build darwin` | `platformUninstall()` — `launchctl unload -w`, remove plist |
| `internal/cmd/status_darwin.go` | `//go:build darwin` | `platformStatus()` — query launchctl, tail log files |
| `internal/cmd/install_darwin_test.go` | `//go:build darwin` | Tests for plist generation, install flow |
| `internal/cmd/uninstall_darwin_test.go` | `//go:build darwin` | Tests for uninstall flow |
| `internal/cmd/status_darwin_test.go` | `//go:build darwin` | Tests for status display |

### Modified Files

| File | Change |
|---|---|
| `internal/cmd/install.go` | Extract shared `Run()` logic (auth validation), call `platformInstall()`. Remove systemd-specific code. |
| `internal/cmd/uninstall.go` | Extract shared `Run()`, call `platformUninstall()`. Remove systemd-specific code. |
| `internal/cmd/status.go` | Extract shared `Run()`, call `platformStatus()`. Remove systemd-specific code. |
| `internal/cmd/root.go` | Update help text to platform-neutral wording |

### Renamed/Moved Files

| From | To | Build tag |
|---|---|---|
| `internal/cmd/install.go` (systemd parts) | `internal/cmd/install_linux.go` | `//go:build linux` |
| `internal/cmd/uninstall.go` (systemd parts) | `internal/cmd/uninstall_linux.go` | `//go:build linux` |
| `internal/cmd/status.go` (systemd parts) | `internal/cmd/status_linux.go` | `//go:build linux` |
| `internal/cmd/systemd_test.go` | Split into `install_linux_test.go`, `uninstall_linux_test.go`, `status_linux_test.go` | `//go:build linux` |

## Launch Agent Plist Template

Written to `~/Library/LaunchAgents/com.obsync.<vault-id>.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.obsync.{{.VaultID}}</string>
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
        <string>/usr/local/bin:/usr/bin:/bin</string>
    </dict>
</dict>
</plist>
```

## Platform-Specific Functions

### `platformInstall(vaultID, vaultPath, binPath string) (string, error)`

**Darwin:**
1. Resolve `vaultPath` to absolute
2. Create `~/Library/Logs/obsync/` directory
3. Render plist template
4. Write to `~/Library/LaunchAgents/com.obsync.<vault-id>.plist`
5. Run `launchctl load -w <plist-path>`
6. Return plist path

**Linux:** Current systemd logic (generate unit file, write, daemon-reload, enable, start).

### `platformUninstall(vaultID string) (string, error)`

**Darwin:**
1. Run `launchctl unload -w <plist-path>`
2. Remove plist file
3. Leave log files intact
4. Return plist path

**Linux:** Current systemd logic (stop, disable, remove, daemon-reload).

### `platformStatus(vaultID string) error`

**Darwin:**
1. Check if plist file exists — if not, report "not installed"
2. Run `launchctl list <label>` to get PID and last exit status
3. Display: label, plist path, running state, PID, last exit code
4. If running: success message
5. If not running with non-zero exit: error + tail last 20 lines from stderr log

**Linux:** Current systemd logic (query ActiveState, SubState, LoadState).

### Testability

`var runLaunchctl` function variable for mocking in tests (mirrors existing `var runSystemctl` pattern).

## Testing

### Darwin Tests

- `TestGeneratePlistFile` — correct XML content
- `TestGeneratePlistFile_RelativePath` — absolute path resolution
- `TestPlistPath` — correct `~/Library/LaunchAgents/` location
- `TestInstallCmd_WritesPlistFile` — mock launchctl, verify file written
- `TestUninstallCmd_RemovesPlistFile` — verify removal and launchctl unload
- `TestStatusCmd_ServiceNotFound_Darwin` — plist missing
- `TestStatusCmd_ServiceRunning_Darwin` — mock PID output
- `TestStatusCmd_ServiceFailed_Darwin` — mock error + log tail

### Linux Tests

Existing `systemd_test.go` tests split into per-command files with `//go:build linux` tags.

### CI

No changes to GoReleaser or GitHub Actions. Linux tests run in CI. macOS tests run locally.

## Build & Distribution Impact

None. GoReleaser already cross-compiles for macOS (amd64, arm64). Build tags are resolved at compile time. Homebrew formula unchanged.
