// Package secrets encrypts customer cloud credentials at rest.
//
// The control plane launches runners with credentials customers paste into
// the dashboard, so the database holds them. They are sealed with
// AES-256-GCM under a key that lives only in the api's environment
// (CREDENTIALS_KEY): a database dump alone yields nothing usable. Each
// blob is bound to the row it belongs to, so one cannot be moved to
// another row and opened there.
//
// CREDENTIALS_KEY may list several keys separated by commas: the first
// seals, every one opens. To rotate, put the new key first, deploy, and
// re-save (reconnect) what the old key sealed before removing it.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeyBytes is the AES-256 key length.
const KeyBytes = 32

// version prefixes every blob so the format can change later.
const version = "v1"

// Keyring seals with its first key and opens with any of them.
type Keyring struct {
	aeads []cipher.AEAD
}

// ParseKey decodes a base64 (standard or URL, padded or not) 32-byte key.
func ParseKey(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
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

// ParseKeys decodes a comma-separated list of keys, newest first.
func ParseKeys(encoded string) ([][]byte, error) {
	var keys [][]byte
	for i, part := range strings.Split(encoded, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, err := ParseKey(part)
		if err != nil {
			return nil, fmt.Errorf("key %d: %w", i+1, err)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil, errors.New("no credentials key")
	}

	return keys, nil
}

// NewKey returns a fresh random key, base64 encoded, for configuration.
func NewKey() string {
	var key [KeyBytes]byte
	_, _ = rand.Read(key[:])

	return base64.StdEncoding.EncodeToString(key[:])
}

// New returns a Keyring that seals with keys[0] and opens with any.
func New(keys ...[]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("no credentials key")
	}
	k := &Keyring{}
	for _, key := range keys {
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
		k.aeads = append(k.aeads, aead)
	}

	return k, nil
}

// associated binds a blob to the row it is stored in: the same context
// must be given to Open.
func associated(context string) []byte {
	return []byte(version + "\x00" + context)
}

// Seal encrypts plain for the row named by context. The blob is
// version || nonce || ciphertext.
func (k *Keyring) Seal(plain []byte, context string) ([]byte, error) {
	aead := k.aeads[0]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	out := make([]byte, 0, len(version)+len(nonce)+len(plain)+aead.Overhead())
	out = append(out, version...)
	out = append(out, nonce...)

	return aead.Seal(out, nonce, plain, associated(context)), nil
}

// Open decrypts a blob from Seal, given the same context.
func (k *Keyring) Open(blob []byte, context string) ([]byte, error) {
	for _, aead := range k.aeads {
		nonceSize := aead.NonceSize()
		if len(blob) < len(version)+nonceSize+aead.Overhead() || string(blob[:len(version)]) != version {
			return nil, errors.New("sealed secret is malformed")
		}
		nonce := blob[len(version) : len(version)+nonceSize]
		plain, err := aead.Open(nil, nonce, blob[len(version)+nonceSize:], associated(context))
		if err == nil {
			return plain, nil
		}
	}

	return nil, errors.New("sealed secret cannot be opened: wrong CREDENTIALS_KEY, another row's secret, or corrupt data")
}
