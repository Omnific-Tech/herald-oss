// Package crypto holds Herald's credential and secret handling.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const tokenBytes = 32

// NewToken returns a URL-safe random credential.
func NewToken() (string, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// HashToken returns the storage form of a token.
//
// SHA-256, not a password hash: these are 256 bits of machine entropy, not
// passwords, so there is nothing to brute force and every authenticated request
// would otherwise pay a deliberate work factor.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Cipher encrypts secrets that must be recoverable, currently the per
// application VAPID private key.
//
// Ciphertext is tagged with the id of the key that produced it, so a rotation
// can decrypt with retired keys while writing with the current one. Without the
// tag, a rotated key would make every stored VAPID private key unreadable and
// silently end Web Push for every application.
type Cipher struct {
	primaryID string
	keys      map[string]cipher.AEAD
}

// NewCipher builds a cipher from a base64 encoded 32 byte primary key and any
// number of retired keys that must still decrypt.
func NewCipher(primaryKey string, retiredKeys ...string) (*Cipher, error) {
	primaryID, primaryAEAD, err := load(primaryKey)
	if err != nil {
		return nil, err
	}

	keys := map[string]cipher.AEAD{primaryID: primaryAEAD}
	for _, retired := range retiredKeys {
		if strings.TrimSpace(retired) == "" {
			continue
		}
		id, aead, err := load(retired)
		if err != nil {
			return nil, fmt.Errorf("retired key: %w", err)
		}
		keys[id] = aead
	}

	return &Cipher{primaryID: primaryID, keys: keys}, nil
}

func load(encodedKey string) (string, cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil {
		return "", nil, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(key) != 32 {
		return "", nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", nil, fmt.Errorf("new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", nil, fmt.Errorf("new gcm: %w", err)
	}

	return KeyID(key), aead, nil
}

// KeyID is a short non-secret label for a key, used to tag its ciphertext.
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:4])
}

// PrimaryKeyID is the id written into new ciphertext.
func (c *Cipher) PrimaryKeyID() string { return c.primaryID }

// NewEncryptionKey returns a fresh base64 encoded key for NewCipher.
func NewEncryptionKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate encryption key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// Encrypt returns "<key id>.<base64 nonce and ciphertext>", bound to
// associatedData so a ciphertext cannot be moved to another row.
func (c *Cipher) Encrypt(plaintext, associatedData string) (string, error) {
	aead := c.keys[c.primaryID]

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := aead.Seal(nonce, nonce, []byte(plaintext), []byte(associatedData))
	return c.primaryID + "." + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt using whichever key produced the ciphertext.
func (c *Cipher) Decrypt(encoded, associatedData string) (string, error) {
	keyID, body, found := strings.Cut(encoded, ".")
	if !found {
		return "", errors.New("ciphertext is missing its key id")
	}

	aead, known := c.keys[keyID]
	if !known {
		return "", fmt.Errorf("no key %s is configured", keyID)
	}

	sealed, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	if len(sealed) < aead.NonceSize() {
		return "", errors.New("ciphertext is too short")
	}

	nonce, remainder := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	plaintext, err := aead.Open(nil, nonce, remainder, []byte(associatedData))
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// NeedsRewrap reports whether a ciphertext was written with a retired key.
func (c *Cipher) NeedsRewrap(encoded string) bool {
	keyID, _, found := strings.Cut(encoded, ".")
	return !found || keyID != c.primaryID
}
