package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      uint32 = 64 * 1024 // 64 MiB
	argonIterations  uint32 = 3
	argonParallelism uint8  = 2
	saltLength              = 16
	keyLength               = 32
)

// HashPassword converts a plaintext password into a salted Argon2id hash.
// The returned string contains the parameters needed for later verification.
// Never store or log the original password.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	// Generate a cryptographically secure, unique salt for this password.
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		argonIterations,
		argonMemory,
		argonParallelism,
		keyLength,
	)

	// Store algorithm parameters, salt, and hash together.
	// Raw Base64 avoids unnecessary padding characters.
	b64 := base64.RawStdEncoding

	encoded := fmt.Sprintf(
		"argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		b64.EncodeToString(salt),
		b64.EncodeToString(hash),
	)

	return encoded, nil
}

// VerifyPassword checks a candidate password against a stored Argon2id hash.
// It returns false for malformed or unsupported stored hashes.
func VerifyPassword(password, encodedHash string) (bool, error) {
	if password == "" {
		return false, nil
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false, nil
	}

	var memory, iterations uint64
	var parallelism uint64

	for _, param := range strings.Split(parts[2], ",") {
		keyValue := strings.SplitN(param, "=", 2)
		if len(keyValue) != 2 {
			return false, nil
		}

		value, err := strconv.ParseUint(keyValue[1], 10, 32)
		if err != nil {
			return false, nil
		}

		switch keyValue[0] {
		case "m":
			memory = value
		case "t":
			iterations = value
		case "p":
			parallelism = value
		default:
			return false, nil
		}
	}

	// Reject unreasonable parameters before allocating memory or CPU.
	// This prevents a corrupted database hash from requesting excessive work.
	if memory < 8*1024 || memory > 128*1024 ||
		iterations < 1 || iterations > 10 ||
		parallelism < 1 || parallelism > 8 {
		return false, nil
	}

	b64 := base64.RawStdEncoding

	salt, err := b64.DecodeString(parts[3])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, nil
	}

	expectedHash, err := b64.DecodeString(parts[4])
	if err != nil || len(expectedHash) != keyLength {
		return false, nil
	}

	actualHash := argon2.IDKey(
		[]byte(password),
		salt,
		uint32(iterations),
		uint32(memory),
		uint8(parallelism),
		uint32(len(expectedHash)),
	)

	// Constant-time comparison avoids leaking how many bytes matched.
	return subtle.ConstantTimeCompare(actualHash, expectedHash) == 1, nil
}
