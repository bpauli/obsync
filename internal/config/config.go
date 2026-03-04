package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	appName    = "obsync"
	configFile = "config.json"
	keyringDir = "keyring"
)

// Config holds the application configuration.
type Config struct {
	Email          string `json:"email,omitempty"`
	Device         string `json:"device,omitempty"`
	KeyringBackend string `json:"keyring_backend,omitempty"`
}

// Load reads a config from the given path. If the file does not exist,
// a Config with sensible defaults is returned.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults()
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	if cfg.Device == "" {
		hostname, _ := os.Hostname()
		cfg.Device = hostname
	}

	return cfg, nil
}

// Save writes the config to the given path atomically (write tmp + rename).
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("ensure config dir: %w", err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	b = append(b, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("commit config: %w", err)
	}

	return nil
}

// ConfigDir returns the default config directory (~/.config/obsync/).
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve config dir: %w", err)
	}
	return filepath.Join(dir, appName), nil
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFile), nil
}

// EnsureKeyringDir creates and returns the keyring directory (~/.config/obsync/keyring/).
func EnsureKeyringDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}

	kd := filepath.Join(dir, keyringDir)
	if err := os.MkdirAll(kd, 0o700); err != nil {
		return "", fmt.Errorf("ensure keyring dir: %w", err)
	}

	return kd, nil
}

func defaults() (Config, error) {
	hostname, _ := os.Hostname()
	return Config{
		Device: hostname,
	}, nil
}
