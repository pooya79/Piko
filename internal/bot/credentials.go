package bot

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

func EncryptionKey(value, sessionSecret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("BOT_ENCRYPTION_KEY must be base64 encoding of 32 random bytes")
	}
	if value == sessionSecret || string(key) == sessionSecret {
		return nil, errors.New("BOT_ENCRYPTION_KEY must differ from SESSION_SECRET")
	}
	return key, nil
}

type credentials struct{ aead cipher.AEAD }

func newCredentials(key []byte) (credentials, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return credentials{}, err
	}
	aead, err := cipher.NewGCM(block)
	return credentials{aead: aead}, err
}

func (c credentials) seal(token string, ownerID, telegramID int64) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, errors.New("generate credential nonce")
	}
	// Bind credentials to their owner and Telegram identity to prevent row swapping.
	aad := []byte(fmt.Sprintf("piko:bot:v1:%d:%d", ownerID, telegramID))
	return c.aead.Seal(nonce, nonce, []byte(token), aad), nil
}
