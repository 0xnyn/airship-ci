package e2b

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefaultDomain is E2B's hosted service: the API is api.<domain> and each
// sandbox's daemon <port>-<sandbox>.<domain>.
const DefaultDomain = "e2b.app"

const maxSecretBytes = 4 << 10

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// Secret is the credential a customer connects: a team API key, and the
// domain of the service it belongs to (the flavour's hosted service unless
// they run their own).
type Secret struct {
	APIKey string `json:"apiKey"`
	Domain string `json:"domain,omitempty"`
}

// EncodeSecret validates an E2B credential and returns it for sealing.
func EncodeSecret(s Secret) ([]byte, error) { return FlavorE2B.EncodeSecret(s) }

// ParseSecret reads a sealed E2B credential.
func ParseSecret(raw []byte) (Secret, error) { return FlavorE2B.ParseSecret(raw) }

// EncodeSecret validates a credential for this flavour and returns it for
// sealing.
func (f Flavor) EncodeSecret(s Secret) ([]byte, error) {
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.Domain = strings.ToLower(strings.Trim(strings.TrimSpace(s.Domain), "."))
	if s.Domain == "" {
		s.Domain = f.DefaultDomain
	}
	if !f.KeyPattern.MatchString(s.APIKey) {
		return nil, fmt.Errorf("%s is required", f.KeyHint)
	}
	if !domainPattern.MatchString(s.Domain) {
		return nil, fmt.Errorf("%q is not a domain (such as %s)", s.Domain, f.DefaultDomain)
	}
	out, err := json.Marshal(s) //nolint:gosec // this is the plaintext the caller seals with internal/secrets before storing it
	if err != nil {
		return nil, fmt.Errorf("encode secret: %w", err)
	}

	return out, nil
}

// ParseSecret reads a sealed credential of this flavour.
func (f Flavor) ParseSecret(raw []byte) (Secret, error) {
	if len(raw) == 0 || len(raw) > maxSecretBytes {
		return Secret{}, errors.New("credential is empty or too large")
	}
	var s Secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return Secret{}, fmt.Errorf("credential is not a %s credential", f.Title)
	}
	if !f.KeyPattern.MatchString(s.APIKey) {
		return Secret{}, errors.New("credential has no API key")
	}
	if s.Domain == "" {
		s.Domain = f.DefaultDomain
	}

	return s, nil
}

// MaskKey is the public half of an API key: its first four and last four
// characters.
func MaskKey(key string) string {
	if len(key) < 12 {
		return "***"
	}

	return key[:4] + "***" + key[len(key)-4:]
}
