package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedSecretPrefix = "enc:v1:"

type secretCipher struct{ aead cipher.AEAD }

func newSecretCipher(key []byte) (*secretCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("secret encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize secret encryption: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize secret authentication: %w", err)
	}
	return &secretCipher{aead: aead}, nil
}

func (cipher *secretCipher) encrypt(plaintext string) (string, error) {
	if cipher == nil {
		return plaintext, nil
	}
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}
	sealed := cipher.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedSecretPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (cipher *secretCipher) decrypt(stored string) (string, error) {
	// One-way and legacy references are not executable provider credentials.
	// Callers that own an explicit legacy migration path may retain the original
	// value themselves; this primitive only releases authenticated ciphertext.
	if !strings.HasPrefix(stored, encryptedSecretPrefix) {
		return "", nil
	}
	if cipher == nil {
		return "", errors.New("encrypted secret cannot be read without the process key")
	}
	encoded := strings.TrimPrefix(stored, encryptedSecretPrefix)
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < cipher.aead.NonceSize() {
		return "", errors.New("stored encrypted secret is invalid")
	}
	nonce, ciphertext := sealed[:cipher.aead.NonceSize()], sealed[cipher.aead.NonceSize():]
	plaintext, err := cipher.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("stored encrypted secret authentication failed")
	}
	return string(plaintext), nil
}
