package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMemory      = 64 * 1024
	passwordIterations  = 3
	passwordParallelism = 2
	passwordSaltBytes   = 16
	passwordHashBytes   = 32
)

// HashPassword returns an Argon2id PHC string suitable for ADMIN_PASSWORD_HASH.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, passwordIterations, passwordMemory, passwordParallelism, passwordHashBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, passwordMemory, passwordIterations, passwordParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword compares a password with an Argon2id PHC string.
func VerifyPassword(password, encoded string) (bool, error) {
	memory, iterations, parallelism, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// ValidatePasswordHash checks the PHC encoding and bounded Argon2 parameters
// without performing the expensive password derivation.
func ValidatePasswordHash(encoded string) error {
	_, _, _, _, _, err := parsePasswordHash(encoded)
	return err
}

func parsePasswordHash(encoded string) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, errors.New("invalid Argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return 0, 0, 0, nil, nil, errors.New("unsupported Argon2id version")
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return 0, 0, 0, nil, nil, errors.New("invalid Argon2id parameters")
	}
	if memory == 0 || memory > 1<<20 || iterations == 0 || iterations > 10 || parallelism == 0 || parallelism > 32 {
		return 0, 0, 0, nil, nil, errors.New("unsafe Argon2id parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return 0, 0, 0, nil, nil, errors.New("invalid Argon2id salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return 0, 0, 0, nil, nil, errors.New("invalid Argon2id digest")
	}
	return memory, iterations, parallelism, salt, expected, nil
}
