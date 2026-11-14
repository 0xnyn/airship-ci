package fleet

import (
	"github.com/wizenheimer/airship/apps/api/internal/cloud/daytona"
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
