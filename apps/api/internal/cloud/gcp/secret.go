package gcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// maxSecretBytes bounds a pasted key file. Real ones are about 2.5 KB.
const maxSecretBytes = 16 << 10

var (
	projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	emailPattern   = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[a-z]{2,}$`)
)

// Secret is the credential a customer connects: a service account key
// file, as the Cloud console downloads it. The whole file is kept, because
// the SDK authenticates with it as is; these are the fields read here.
type Secret struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
	TokenURI     string `json:"token_uri"`
}

// EncodeSecret validates a service account key file and returns it
// compacted, for sealing.
func EncodeSecret(raw []byte) ([]byte, error) {
	if _, err := ParseSecret(raw); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return nil, fmt.Errorf("compact key file: %w", err)
	}

	return out.Bytes(), nil
}

// ParseSecret reads the fields the provider needs from a key file and
// checks it is a service account key.
func ParseSecret(raw []byte) (Secret, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Secret{}, errors.New("a service account key file is required")
	}
	if len(raw) > maxSecretBytes {
		return Secret{}, fmt.Errorf("key file is %d bytes; a service account key is under %d", len(raw), maxSecretBytes)
	}
	var s Secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return Secret{}, errors.New("key file is not JSON")
	}
	switch {
	case s.Type != "service_account":
		return Secret{}, fmt.Errorf("key file type is %q; a service account key has type service_account", s.Type)
	case !projectPattern.MatchString(s.ProjectID):
		return Secret{}, errors.New("key file has no valid project_id")
	case !emailPattern.MatchString(s.ClientEmail):
		return Secret{}, errors.New("key file has no valid client_email")
	case !strings.Contains(s.PrivateKey, "PRIVATE KEY-----"):
		return Secret{}, errors.New("key file has no private_key")
	}

	return s, nil
}
