package runloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// DefaultAPIURL is Runloop's hosted API.
const DefaultAPIURL = "https://api.runloop.ai"

const maxSecretBytes = 4 << 10

// API keys are opaque; this only refuses whitespace and separators that
// could not be a key.
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{16,512}$`)

// Secret is the credential a customer connects: a Runloop API key, and the
// API it belongs to (the hosted service unless they were given another).
type Secret struct {
	APIKey string `json:"apiKey"`
	APIURL string `json:"apiUrl,omitempty"`
}

// EncodeSecret validates a credential and returns it for sealing. The URL
// is normalized so the same key and server always seal the same bytes.
func EncodeSecret(s Secret) ([]byte, error) {
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.APIURL = strings.TrimRight(strings.TrimSpace(s.APIURL), "/")
	if s.APIURL == "" {
		s.APIURL = DefaultAPIURL
	}
	if !keyPattern.MatchString(s.APIKey) {
		return nil, errors.New("an API key is required")
	}
	u, err := url.Parse(s.APIURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("%q is not an API URL (such as %s)", s.APIURL, DefaultAPIURL)
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
		return Secret{}, errors.New("credential is not a Runloop credential")
	}
	if !keyPattern.MatchString(s.APIKey) {
		return Secret{}, errors.New("credential has no API key")
	}
	if s.APIURL == "" {
		s.APIURL = DefaultAPIURL
	}

	return s, nil
}

// MaskKey is the public half of an API key: its first and last three
// characters.
func MaskKey(key string) string {
	if len(key) < 12 {
		return "***"
	}

	return key[:3] + "***" + key[len(key)-3:]
}
