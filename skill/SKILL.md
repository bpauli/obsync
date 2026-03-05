---
name: obsync
description: Obsidian Sync CLI for syncing vaults on headless Linux servers with full end-to-end encryption.
homepage: https://github.com/bpauli/obsync
metadata: {"clawdbot":{"emoji":"🔄","os":["darwin","linux"],"requires":{"bins":["obsync"]},"install":[{"id":"homebrew","kind":"brew","formula":"bpauli/tap/obsync","bins":["obsync"],"label":"Homebrew (recommended)"},{"id":"source","kind":"source","url":"https://github.com/bpauli/obsync","bins":["obsync"],"label":"Build from source (Go 1.25+)"}]}}
---

# obsync

Use `obsync` to sync Obsidian vaults on headless Linux servers. Requires an Obsidian Sync subscription and account credentials.

Setup (once)

- `obsync login` (enter email, password, and optional MFA code)
- For headless servers: `export OBSYNC_KEYRING_BACKEND=file`
- Optionally set keyring password: `export OBSYNC_KEYRING_PASSWORD=mysecret`
- Verify: `obsync list`

Output

- Default: human-friendly terminal output with colors and spinners.
- Use `--json` / `-j` for JSON output.
- Use `--verbose` / `-v` for debug logging.

Common commands

- Log in: `obsync login`
- List vaults: `obsync list`
- Pull vault: `obsync pull "My Notes" ~/notes -p "e2e-password"`
- Pull and save password: `obsync pull "My Notes" ~/notes -p "e2e-password" -s`
- Push local changes: `obsync push "My Notes" ~/notes -p "e2e-password"`
- Watch (continuous sync): `obsync watch "My Notes" ~/notes -p "e2e-password"`
- Install systemd service: `obsync install "My Notes" ~/notes`
- Check service status: `obsync status "My Notes"`
- View service logs: `journalctl --user -u obsync@<vault-id>.service -f`
- Uninstall service: `obsync uninstall "My Notes"`

Flags

- `-p, --password` — E2E encryption password
- `-s, --save-password` — save E2E password to keyring for future use
- `-v, --verbose` — enable debug logging
- `-j, --json` — JSON output to stdout
- `--config` — path to config file (or `OBSYNC_CONFIG` env var)
- `--version` — print version and exit

Notes

- Requires a valid Obsidian Sync subscription.
- E2E encryption uses AES-256-GCM with scrypt key derivation.
- Pull/push compare files by SHA-256 hash — only changed files are transferred.
- Watch mode uses WebSocket for remote changes and fsnotify for local changes (500ms debounce).
- Large files are handled with 2MB chunked transfers.
- Automatic reconnection with exponential backoff (1s-60s) on connection loss.
- The `.obsidian/` directory (themes, plugins, settings) is synced.
- For headless servers without a desktop keyring, use `OBSYNC_KEYRING_BACKEND=file`.
- For always-on sync on headless servers, enable lingering: `loginctl enable-linger $USER`.
- Config is stored at `~/.config/obsync/config.json`.
