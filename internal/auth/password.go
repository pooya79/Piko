package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const maxPasswordBytes = 1024

var ErrInvalidCredentials = errors.New("invalid email or password")
var ErrInvalidPassword = errors.New("invalid password")

type argonParams struct {
	memory                uint32
	iterations            uint32
	parallelism           uint8
	saltLength, keyLength uint32
}

var passwordParams = argonParams{memory: 64 * 1024, iterations: 3, parallelism: 2, saltLength: 16, keyLength: 32}

// HashPassword validates the registration rule before hashing.
func HashPassword(password string) (string, error) {
	if len(password) > maxPasswordBytes {
		return "", fmt.Errorf("%w: too long", ErrInvalidPassword)
	}
	var hasLetter, hasDigit bool
	for _, r := range password {
		hasLetter = hasLetter || unicode.IsLetter(r)
		hasDigit = hasDigit || unicode.IsDigit(r)
	}
	if utf8.RuneCountInString(password) < 6 || !hasLetter || !hasDigit {
		return "", fmt.Errorf("%w: must be at least 6 characters and include a letter and a number", ErrInvalidPassword)
	}
	return hashPassword(password)
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordParams.saltLength)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	hash := argon2.IDKey([]byte(password), salt, passwordParams.iterations, passwordParams.memory, passwordParams.parallelism, passwordParams.keyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", passwordParams.memory, passwordParams.iterations, passwordParams.parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword bounds the cost parameters read from an untrusted encoded hash.
func VerifyPassword(encoded, password string) bool {
	if len(password) > maxPasswordBytes {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint64
	var p64 uint64
	for _, v := range strings.Split(parts[3], ",") {
		kv := strings.SplitN(v, "=", 2)
		if len(kv) != 2 {
			continue
		}
		n, e := strconv.ParseUint(kv[1], 10, 32)
		if e != nil {
			return false
		}
		switch kv[0] {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			p64 = n
		}
	}
	if m > 256*1024 || t > 10 || p64 > 16 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(m), uint8(p64), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
