package fleet

import (
	"errors"

	"github.com/wizenheimer/airship/apps/api/internal/cloud/daytona"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/e2b"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/modal"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/namespace"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/runloop"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/tenki"
)

// daytonaSecret returns a Daytona API key's masked form, as Daytona itself
// shows keys.
func daytonaSecret(secret []byte) (string, error) {
	parsed, err := daytona.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return daytona.MaskKey(parsed.APIKey), nil
}

// runloopSecret returns a Runloop API key's masked form.
func runloopSecret(secret []byte) (string, error) {
	parsed, err := runloop.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return runloop.MaskKey(parsed.APIKey), nil
}

// e2bSecret returns the masked API key of an E2B-protocol cloud's
// credential (E2B, Novita, Lelantos).
func e2bSecret(cloudName string, secret []byte) (string, error) {
	flavor, ok := e2b.FlavorFor(cloudName)
	if !ok {
		return "", errors.New("not an E2B-protocol cloud")
	}
	parsed, err := flavor.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return e2b.MaskKey(parsed.APIKey), nil
}

// namespaceSecret returns a Namespace token's masked form.
func namespaceSecret(secret []byte) (string, error) {
	parsed, err := namespace.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return namespace.MaskKey(parsed.Token), nil
}

// tenkiSecret returns a Tenki API key's masked form.
func tenkiSecret(secret []byte) (string, error) {
	parsed, err := tenki.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return tenki.MaskKey(parsed.APIKey), nil
}

// modalSecret returns a Modal token's ID, its public half.
func modalSecret(secret []byte) (string, error) {
	parsed, err := modal.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return parsed.TokenID, nil
}
