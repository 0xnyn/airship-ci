package modal

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const maxSecretBytes = 4 << 10

var (
	// Modal tokens are a pair: ak-... and as-....
	tokenIDPattern     = regexp.MustCompile(`^ak-[A-Za-z0-9]{8,128}$`)
	tokenSecretPattern = regexp.MustCompile(`^as-[A-Za-z0-9]{8,128}$`)
	environmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// Secret is the credential a customer connects: a Modal token pair, and the
// environment to run in (the workspace's default when empty).
type Secret struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
	Environment string `json:"environment,omitempty"`
}

// EncodeSecret validates a credential and returns it for sealing.
func EncodeSecret(s Secret) ([]byte, error) {
	s.TokenID = strings.TrimSpace(s.TokenID)
	s.TokenSecret = strings.TrimSpace(s.TokenSecret)
	s.Environment = strings.TrimSpace(s.Environment)
	if !tokenIDPattern.MatchString(s.TokenID) {
		return nil, errors.New("a token ID (ak-...) is required")
	}
	if !tokenSecretPattern.MatchString(s.TokenSecret) {
		return nil, errors.New("a token secret (as-...) is required")
	}
	if s.Environment != "" && !environmentPattern.MatchString(s.Environment) {
		return nil, fmt.Errorf("%q is not a Modal environment name", s.Environment)
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
		return Secret{}, errors.New("credential is not a Modal credential")
	}
	if !tokenIDPattern.MatchString(s.TokenID) || !tokenSecretPattern.MatchString(s.TokenSecret) {
		return Secret{}, errors.New("credential has no token")
	}

	return s, nil
}
