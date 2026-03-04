package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestDeriveKey(t *testing.T) {
	key, err := DeriveKey("password123", "salt456")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key))
	}

	// Same inputs produce same key (deterministic).
	key2, err := DeriveKey("password123", "salt456")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if !bytes.Equal(key, key2) {
		t.Fatal("same inputs should produce same key")
	}

	// Different inputs produce different keys.
	key3, err := DeriveKey("password123", "different_salt")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if bytes.Equal(key, key3) {
		t.Fatal("different inputs should produce different keys")
	}
}

func TestDeriveKey_NFKC(t *testing.T) {
	// U+2126 OHM SIGN normalizes to U+03A9 GREEK CAPITAL LETTER OMEGA under NFKC.
	key1, err := DeriveKey("\u2126", "salt")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	key2, err := DeriveKey("\u03A9", "salt")
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	if !bytes.Equal(key1, key2) {
		t.Fatal("NFKC-equivalent passwords should produce same key")
	}
}

func TestKeyHash(t *testing.T) {
	key := make([]byte, 32)
	hash := KeyHash(key)
	if hash == "" {
		t.Fatal("KeyHash returned empty string")
	}
	// Same key should produce same hash.
	if KeyHash(key) != hash {
		t.Fatal("KeyHash is not deterministic")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("hello, obsidian sync!")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext should differ from plaintext")
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("round-trip failed: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncrypt_RandomNonce(t *testing.T) {
	key := testKey(t)
	plaintext := []byte("same data")

	ct1, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct2, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("two encryptions of same plaintext should produce different ciphertexts")
	}
}

func TestEncryptDecrypt_EmptyPlaintext(t *testing.T) {
	key := testKey(t)
	ciphertext, err := Encrypt(key, []byte{})
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(decrypted) != 0 {
		t.Fatalf("expected empty plaintext, got %d bytes", len(decrypted))
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	key := testKey(t)
	_, err := Decrypt(key, []byte("short"))
	if err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1 := testKey(t)
	key2 := testKey(t)
	plaintext := []byte("secret")

	ciphertext, err := Encrypt(key1, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = Decrypt(key2, ciphertext)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestEncryptDecryptPath_RoundTrip(t *testing.T) {
	key := testKey(t)
	path := "notes/daily/2024-03-04.md"

	encrypted, err := EncryptPath(key, path)
	if err != nil {
		t.Fatalf("EncryptPath: %v", err)
	}
	if encrypted == "" {
		t.Fatal("EncryptPath returned empty string")
	}
	if encrypted == path {
		t.Fatal("encrypted path should differ from plaintext")
	}

	decrypted, err := DecryptPath(key, encrypted)
	if err != nil {
		t.Fatalf("DecryptPath: %v", err)
	}
	if decrypted != path {
		t.Fatalf("round-trip failed: got %q, want %q", decrypted, path)
	}
}

func TestEncryptPath_Deterministic(t *testing.T) {
	key := testKey(t)
	path := "notes/test.md"

	enc1, err := EncryptPath(key, path)
	if err != nil {
		t.Fatalf("EncryptPath: %v", err)
	}
	enc2, err := EncryptPath(key, path)
	if err != nil {
		t.Fatalf("EncryptPath: %v", err)
	}
	if enc1 != enc2 {
		t.Fatal("EncryptPath should be deterministic for same key and path")
	}
}

func TestEncryptPath_DifferentPaths(t *testing.T) {
	key := testKey(t)

	enc1, err := EncryptPath(key, "path/a.md")
	if err != nil {
		t.Fatalf("EncryptPath: %v", err)
	}
	enc2, err := EncryptPath(key, "path/b.md")
	if err != nil {
		t.Fatalf("EncryptPath: %v", err)
	}
	if enc1 == enc2 {
		t.Fatal("different paths should produce different encrypted values")
	}
}

func TestDecryptPath_InvalidBase64(t *testing.T) {
	key := testKey(t)
	_, err := DecryptPath(key, "not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestEncryptDecrypt_LargeData(t *testing.T) {
	key := testKey(t)
	plaintext := make([]byte, 2*1024*1024) // 2MB, matching Obsidian chunk size
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("large data round-trip failed")
	}
}
