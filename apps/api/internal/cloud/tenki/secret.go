package tenki

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// DefaultAPIURL is Tenki's hosted API.
const DefaultAPIURL = "https://api.tenki.cloud"

const maxSecretBytes = 4 << 10

var (
	// Tenki keys are tk_... (lx_... since the Luxor rename).
	keyPattern       = regexp.MustCompile(`^(tk|lx)_[A-Za-z0-9_-]{8,512}$`)
	workspacePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// Secret is the credential a customer connects: a workspace API key, the
// API it belongs to, and optionally the workspace (a workspace key implies
// its own).
type Secret struct {
	APIKey      string `json:"apiKey"`
	APIURL      string `json:"apiUrl,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

// EncodeSecret validates a credential and returns it for sealing.
func EncodeSecret(s Secret) ([]byte, error) {
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.APIURL = strings.TrimRight(strings.TrimSpace(s.APIURL), "/")
	s.WorkspaceID = strings.TrimSpace(s.WorkspaceID)
	if s.APIURL == "" {
		s.APIURL = DefaultAPIURL
	}
	if !keyPattern.MatchString(s.APIKey) {
		return nil, errors.New("an API key (tk_... or lx_...) is required")
	}
	u, err := url.Parse(s.APIURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("%q is not an API URL (such as %s)", s.APIURL, DefaultAPIURL)
	}
	if s.WorkspaceID != "" && !workspacePattern.MatchString(s.WorkspaceID) {
		return nil, fmt.Errorf("%q is not a workspace ID", s.WorkspaceID)
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
		return Secret{}, errors.New("credential is not a Tenki credential")
	}
	if !keyPattern.MatchString(s.APIKey) {
		return Secret{}, errors.New("credential has no API key")
	}
	if s.APIURL == "" {
		s.APIURL = DefaultAPIURL
	}

	return s, nil
}

// MaskKey is the public half of an API key: its prefix and last four
// characters.
func MaskKey(key string) string {
	if len(key) < 12 {
		return "***"
	}

	return key[:3] + "***" + key[len(key)-4:]
}
