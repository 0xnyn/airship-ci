// Package secrets encrypts customer cloud credentials at rest.
//
// The control plane launches runners with credentials customers paste into
// the dashboard, so the database holds them. They are sealed with
// AES-256-GCM under a key that lives only in the api's environment
// (CREDENTIALS_KEY): a database dump alone yields nothing usable.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeyBytes is the AES-256 key length.
const KeyBytes = 32

// version prefixes every blob so the format can change later.
const version = "v1"

// Keyring seals and opens secrets with one key.
type Keyring struct {
	aead cipher.AEAD
}

// ParseKey decodes a base64 (standard or URL, padded or not) 32-byte key.
func ParseKey(encoded string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		key, err := enc.DecodeString(encoded)
		if err == nil {
			if len(key) != KeyBytes {
				return nil, fmt.Errorf("credentials key is %d bytes, want %d", len(key), KeyBytes)
			}

			return key, nil
		}
	}

	return nil, errors.New("credentials key is not base64")
}

// NewKey returns a fresh random key, base64 encoded, for configuration.
func NewKey() string {
	var key [KeyBytes]byte
	_, _ = rand.Read(key[:])

	return base64.StdEncoding.EncodeToString(key[:])
}

// New returns a Keyring for key.
func New(key []byte) (*Keyring, error) {
	if len(key) != KeyBytes {
		return nil, fmt.Errorf("credentials key is %d bytes, want %d", len(key), KeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}

	return &Keyring{aead: aead}, nil
}

// Seal encrypts plain. The blob is version || nonce || ciphertext.
func (k *Keyring) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	out := make([]byte, 0, len(version)+len(nonce)+len(plain)+k.aead.Overhead())
	out = append(out, version...)
	out = append(out, nonce...)

	return k.aead.Seal(out, nonce, plain, []byte(version)), nil
}

// Open decrypts a blob from Seal.
func (k *Keyring) Open(blob []byte) ([]byte, error) {
	nonceSize := k.aead.NonceSize()
	if len(blob) < len(version)+nonceSize+k.aead.Overhead() || string(blob[:len(version)]) != version {
		return nil, errors.New("sealed secret is malformed")
	}
	nonce := blob[len(version) : len(version)+nonceSize]
	plain, err := k.aead.Open(nil, nonce, blob[len(version)+nonceSize:], []byte(version))
	if err != nil {
		return nil, errors.New("sealed secret cannot be opened: wrong CREDENTIALS_KEY or corrupt data")
	}

	return plain, nil
}
