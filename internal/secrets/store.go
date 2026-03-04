package secrets

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"

	"github.com/99designs/keyring"

	"obsync/internal/config"
)

const (
	// Environment variable names.
	keyringPasswordEnv = "OBSYNC_KEYRING_PASSWORD"
	keyringBackendEnv  = "OBSYNC_KEYRING_BACKEND"

	serviceName = "obsync"
)

// Store provides secure storage for auth tokens and E2E passwords.
type Store interface {
	SetToken(email string, token string) error
	GetToken(email string) (string, error)
	DeleteToken(email string) error
	SetE2EPassword(vaultUID string, password string) error
	GetE2EPassword(vaultUID string) (string, error)
}

// KeyringStore implements Store using the 99designs/keyring library.
type KeyringStore struct {
	ring keyring.Keyring
}

// tokenKey returns the keyring key for an auth token.
func tokenKey(email string) string {
	return "token:" + email
}

// e2eKey returns the keyring key for an E2E encryption password.
func e2eKey(vaultUID string) string {
	return "e2e:" + vaultUID
}

func (s *KeyringStore) SetToken(email string, token string) error {
	return s.ring.Set(keyring.Item{
		Key:  tokenKey(email),
		Data: []byte(token),
	})
}

func (s *KeyringStore) GetToken(email string) (string, error) {
	item, err := s.ring.Get(tokenKey(email))
	if err != nil {
		return "", fmt.Errorf("get token for %s: %w", email, err)
	}
	return string(item.Data), nil
}

func (s *KeyringStore) DeleteToken(email string) error {
	return s.ring.Remove(tokenKey(email))
}

func (s *KeyringStore) SetE2EPassword(vaultUID string, password string) error {
	return s.ring.Set(keyring.Item{
		Key:  e2eKey(vaultUID),
		Data: []byte(password),
	})
}

func (s *KeyringStore) GetE2EPassword(vaultUID string) (string, error) {
	item, err := s.ring.Get(e2eKey(vaultUID))
	if err != nil {
		return "", fmt.Errorf("get e2e password for vault %s: %w", vaultUID, err)
	}
	return string(item.Data), nil
}

// OpenDefault opens a keyring store with the default configuration,
// resolving the backend from environment variables, config, or auto-detection.
// This is a package-level variable to allow monkey-patching in tests.
var OpenDefault = func() (Store, error) {
	backend, err := resolveBackend()
	if err != nil {
		return nil, err
	}
	return Open(backend)
}

// Open creates a new KeyringStore with the specified backend.
func Open(backend string) (Store, error) {
	backends, err := allowedBackends(backend)
	if err != nil {
		return nil, err
	}

	keyringDir, err := config.EnsureKeyringDir()
	if err != nil {
		return nil, fmt.Errorf("ensure keyring dir: %w", err)
	}

	cfg := keyring.Config{
		ServiceName:             serviceName,
		KeychainTrustApplication: false,
		AllowedBackends:         backends,
		FileDir:                 keyringDir,
		FilePasswordFunc:        filePasswordFunc(),
	}

	ring, err := keyringOpen(cfg)
	if err != nil {
		return nil, fmt.Errorf("open keyring: %w", err)
	}

	return &KeyringStore{ring: ring}, nil
}

// keyringOpen is the function used to open a keyring. It's a variable to
// allow replacement in tests.
var keyringOpen = keyring.Open

// resolveBackend determines the keyring backend from env vars and config.
func resolveBackend() (string, error) {
	// Highest priority: environment variable.
	if env := os.Getenv(keyringBackendEnv); env != "" {
		slog.Debug("keyring backend from env", "backend", env)
		return env, nil
	}

	// Medium priority: config file.
	cfgPath, err := config.DefaultConfigPath()
	if err == nil {
		cfg, err := config.Load(cfgPath)
		if err == nil && cfg.KeyringBackend != "" {
			slog.Debug("keyring backend from config", "backend", cfg.KeyringBackend)
			return cfg.KeyringBackend, nil
		}
	}

	// Default: auto-detect.
	return "auto", nil
}

// allowedBackends translates a backend name to a list of keyring backends.
func allowedBackends(backend string) ([]keyring.BackendType, error) {
	switch backend {
	case "file":
		return []keyring.BackendType{keyring.FileBackend}, nil
	case "keychain":
		return []keyring.BackendType{keyring.KeychainBackend}, nil
	case "kwallet":
		return []keyring.BackendType{keyring.KWalletBackend}, nil
	case "secret-service":
		return []keyring.BackendType{keyring.SecretServiceBackend}, nil
	case "auto", "":
		return autoBackends(), nil
	default:
		return nil, fmt.Errorf("unknown keyring backend: %q", backend)
	}
}

// autoBackends returns the platform-appropriate backends.
func autoBackends() []keyring.BackendType {
	if runtime.GOOS == "darwin" {
		return []keyring.BackendType{keyring.KeychainBackend}
	}

	// On Linux, check if D-Bus is available. If not, fall back to file backend
	// to avoid hangs on headless servers without a desktop session.
	if runtime.GOOS == "linux" && !hasDBus() {
		slog.Debug("D-Bus not available, falling back to file keyring backend")
		return []keyring.BackendType{keyring.FileBackend}
	}

	return keyring.AvailableBackends()
}

// hasDBus checks whether D-Bus is available on the system.
func hasDBus() bool {
	// Check DBUS_SESSION_BUS_ADDRESS env var first.
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
		return true
	}
	// Check if dbus-daemon is available.
	_, err := exec.LookPath("dbus-daemon")
	return err == nil
}

// filePasswordFunc returns a function that provides the password for the
// encrypted file backend.
func filePasswordFunc() keyring.PromptFunc {
	return func(prompt string) (string, error) {
		// Use environment variable if available (for headless/systemd use).
		if pw := os.Getenv(keyringPasswordEnv); pw != "" {
			return pw, nil
		}
		return "", fmt.Errorf("set %s environment variable for headless keyring access", keyringPasswordEnv)
	}
}
