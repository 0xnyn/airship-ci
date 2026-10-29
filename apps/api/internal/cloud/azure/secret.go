package azure

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// maxSecretBytes bounds a connected credential. Real ones are a few hundred
// bytes.
const maxSecretBytes = 4 << 10

// maxClientSecretLength is above what Microsoft Entra issues (a few dozen
// characters) by a wide margin.
const maxClientSecretLength = 1024

var guidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Secret is the credential a customer connects: a Microsoft Entra
// application (service principal) with its client secret, and the
// subscription it has the Contributor role on.
type Secret struct {
	SubscriptionID string `json:"subscription_id"`
	TenantID       string `json:"tenant_id"`
	ClientID       string `json:"client_id"`
	ClientSecret   string `json:"client_secret"`
}

// EncodeSecret validates a credential and returns it as the bytes to seal.
// The IDs are kept lowercase so that the same credential always seals the
// same, and compares equal across connections.
func EncodeSecret(s Secret) ([]byte, error) {
	s.SubscriptionID = strings.ToLower(strings.TrimSpace(s.SubscriptionID))
	s.TenantID = strings.ToLower(strings.TrimSpace(s.TenantID))
	s.ClientID = strings.ToLower(strings.TrimSpace(s.ClientID))
	s.ClientSecret = strings.TrimSpace(s.ClientSecret)
	switch {
	case !guidPattern.MatchString(s.SubscriptionID):
		return nil, errors.New("subscription_id must be a subscription ID (a GUID)")
	case !guidPattern.MatchString(s.TenantID):
		return nil, errors.New("tenant_id must be a directory (tenant) ID (a GUID)")
	case !guidPattern.MatchString(s.ClientID):
		return nil, errors.New("client_id must be an application (client) ID (a GUID)")
	case s.ClientSecret == "":
		return nil, errors.New("client_secret is required")
	case len(s.ClientSecret) > maxClientSecretLength:
		return nil, fmt.Errorf("client_secret is over %d characters", maxClientSecretLength)
	case strings.ContainsAny(s.ClientSecret, "\x00\r\n\t "):
		return nil, errors.New("client_secret contains whitespace or control characters")
	}
	raw, err := json.Marshal(s) //nolint:gosec // the credential is what is being sealed
	if err != nil {
		return nil, fmt.Errorf("encode credential: %w", err)
	}

	return raw, nil
}

// ParseSecret reads a sealed credential's bytes back.
func ParseSecret(raw []byte) (Secret, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Secret{}, errors.New("an Azure credential is required")
	}
	if len(raw) > maxSecretBytes {
		return Secret{}, fmt.Errorf("credential is %d bytes; an Azure credential is under %d", len(raw), maxSecretBytes)
	}
	var s Secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return Secret{}, errors.New("credential is not JSON")
	}
	encoded, err := EncodeSecret(s)
	if err != nil {
		return Secret{}, err
	}
	_ = json.Unmarshal(encoded, &s) // the normalized form

	return s, nil
}
