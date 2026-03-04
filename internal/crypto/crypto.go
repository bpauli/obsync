// Package crypto implements scrypt key derivation and AES-256-GCM encryption
// for the Obsidian Sync protocol.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/text/unicode/norm"
)

// Scrypt parameters matching Obsidian's encryption version 3.
const (
	scryptN   = 32768
	scryptR   = 8
	scryptP   = 1
	keyLen    = 32
	nonceSize = 12 // AES-GCM standard nonce size
)

var (
	errCiphertextTooShort = errors.New("crypto: ciphertext too short")
	errInvalidBase64      = errors.New("crypto: invalid base64 encoding")
)

// DeriveKey derives a 32-byte encryption key from password and salt using
// scrypt with NFKC normalization, matching Obsidian's key derivation.
func DeriveKey(password, salt string) ([]byte, error) {
	pw := norm.NFKC.Bytes([]byte(password))
	s := norm.NFKC.Bytes([]byte(salt))
	key, err := scrypt.Key(pw, s, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, fmt.Errorf("crypto: scrypt key derivation: %w", err)
	}
	return key, nil
}

// KeyHash returns base64(SHA-256(key)), used for vault access authentication.
func KeyHash(key []byte) string {
	h := sha256.Sum256(key)
	return base64.StdEncoding.EncodeToString(h[:])
}

// Encrypt encrypts plaintext using AES-256-GCM with a random 12-byte nonce.
// The nonce is prepended to the ciphertext in the output.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: random nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt decrypts ciphertext produced by Encrypt. It expects the first 12
// bytes to be the nonce.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errCiphertextTooShort
	}
	nonce := ciphertext[:gcm.NonceSize()]
	ct := ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return plaintext, nil
}

// EncryptPath encrypts a path string using AES-256-GCM with a deterministic
// nonce derived from SHA-256(plaintext)[0:12]. Returns base64(nonce||ciphertext).
func EncryptPath(key []byte, path string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}
	plaintext := []byte(path)
	h := sha256.Sum256(plaintext)
	nonce := h[:gcm.NonceSize()]
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptPath decrypts a base64-encoded path produced by EncryptPath.
func DecryptPath(key []byte, encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidBase64, err) //nolint:errorlint
	}
	plaintext, err := Decrypt(key, data)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
