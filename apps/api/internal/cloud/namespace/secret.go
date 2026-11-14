package namespace

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DefaultRegion is where runners launch when neither the config nor the
// credential says.
const DefaultRegion = "us"

const maxSecretBytes = 8 << 10

var (
	// Tokens are opaque (workspace tokens and revokable tokens differ);
	// this refuses only what could not be one.
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_.\-+/=:]{16,1000}$`)
	// Regions are Namespace's regional Compute API endpoints:
	// <region>.compute.namespaceapis.com.
	regions = []string{"us", "eu"}
)

// Regions lists the regions Namespace's Compute API is served in.
func Regions() []string { return append([]string(nil), regions...) }

// IsRegion reports a Namespace region.
func IsRegion(region string) bool {
	for _, r := range regions {
		if r == region {
			return true
		}
	}

	return false
}

// Secret is the credential a customer connects: a Namespace token that
// can create and destroy instances in their workspace.
type Secret struct {
	Token string `json:"token"`
}

// EncodeSecret validates a credential and returns it for sealing.
func EncodeSecret(s Secret) ([]byte, error) {
	s.Token = strings.TrimSpace(s.Token)
	if !tokenPattern.MatchString(s.Token) {
		return nil, errors.New("a Namespace token is required")
	}
	out, err := json.Marshal(s) //nolint:gosec // this is the plaintext the caller seals with internal/secrets before storing it
	if err != nil {
		return nil, fmt.Errorf("encode secret: %w", err)
	}

	return out, nil
}

// ParseSecret reads a sealed credential's contents.
func ParseSecret(raw []byte) (Secret, error) {
	if len(raw) == 0 || len(raw) > maxSecretBytes {
		return Secret{}, errors.New("credential is empty or too large")
	}
	var s Secret
	if err := json.Unmarshal(raw, &s); err != nil {
		return Secret{}, errors.New("credential is not a Namespace credential")
	}
	if !tokenPattern.MatchString(s.Token) {
		return Secret{}, errors.New("credential has no token")
	}

	return s, nil
}

// MaskKey is the public half of a token: its first and last four
// characters.
func MaskKey(key string) string {
	if len(key) < 16 {
		return "***"
	}

	return key[:4] + "***" + key[len(key)-4:]
}
