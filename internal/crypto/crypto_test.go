package crypto_test

import (
	"strings"
	"testing"

	"github.com/Omnific-Tech/herald/internal/crypto"
)

func newCipher(t *testing.T, retired ...string) (*crypto.Cipher, string) {
	t.Helper()

	key, err := crypto.NewEncryptionKey()
	if err != nil {
		t.Fatalf("NewEncryptionKey: %v", err)
	}
	cipher, err := crypto.NewCipher(key, retired...)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return cipher, key
}

func TestNewTokenIsUniqueAndURLSafe(t *testing.T) {
	first, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	second, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	if first == second {
		t.Fatal("two tokens were identical")
	}
	if strings.ContainsAny(first, "+/=") {
		t.Fatalf("token is not url safe: %q", first)
	}
}

func TestHashTokenIsStable(t *testing.T) {
	if crypto.HashToken("abc") != crypto.HashToken("abc") {
		t.Fatal("hash is not stable")
	}
	if crypto.HashToken("abc") == crypto.HashToken("abd") {
		t.Fatal("hash collided on different input")
	}
}

func TestCipherRoundTrip(t *testing.T) {
	cipher, _ := newCipher(t)

	encrypted, err := cipher.Encrypt("vapid-private-key", "app-1")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if strings.Contains(encrypted, "vapid-private-key") {
		t.Fatal("ciphertext contains the plaintext")
	}

	decrypted, err := cipher.Decrypt(encrypted, "app-1")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if decrypted != "vapid-private-key" {
		t.Fatalf("round trip: got %q", decrypted)
	}
}

func TestCipherUsesFreshNonce(t *testing.T) {
	cipher, _ := newCipher(t)

	first, _ := cipher.Encrypt("same", "app-1")
	second, _ := cipher.Encrypt("same", "app-1")

	if first == second {
		t.Fatal("ciphertexts repeated across encryptions")
	}
}

// The associated data binds a ciphertext to its row, so one application's
// encrypted key cannot be pasted into another application's record.
func TestCipherRejectsMovedCiphertext(t *testing.T) {
	cipher, _ := newCipher(t)

	encrypted, err := cipher.Encrypt("vapid-private-key", "app-1")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := cipher.Decrypt(encrypted, "app-2"); err == nil {
		t.Fatal("decrypted a ciphertext bound to another application")
	}
}

// A rotated key must still read what the retired key wrote, or every stored
// VAPID private key becomes unrecoverable.
func TestRetiredKeyStillDecrypts(t *testing.T) {
	oldCipher, oldKey := newCipher(t)

	encrypted, err := oldCipher.Encrypt("vapid-private-key", "app-1")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	rotated, _ := newCipher(t, oldKey)

	decrypted, err := rotated.Decrypt(encrypted, "app-1")
	if err != nil {
		t.Fatalf("Decrypt after rotation: %v", err)
	}
	if decrypted != "vapid-private-key" {
		t.Fatalf("round trip after rotation: got %q", decrypted)
	}
	if !rotated.NeedsRewrap(encrypted) {
		t.Fatal("ciphertext from a retired key was not flagged for rewrap")
	}

	rewrapped, err := rotated.Encrypt(decrypted, "app-1")
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if rotated.NeedsRewrap(rewrapped) {
		t.Fatal("freshly written ciphertext was flagged for rewrap")
	}
}

func TestDecryptRejectsUnknownKey(t *testing.T) {
	first, _ := newCipher(t)
	second, _ := newCipher(t)

	encrypted, err := first.Encrypt("secret", "app-1")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := second.Decrypt(encrypted, "app-1"); err == nil {
		t.Fatal("decrypted with a cipher that has no matching key")
	}
}

func TestNewCipherRejectsShortKey(t *testing.T) {
	if _, err := crypto.NewCipher("c2hvcnQ="); err == nil {
		t.Fatal("NewCipher accepted a short key")
	}
}
