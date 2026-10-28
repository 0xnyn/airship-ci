package fleet

import (
	"github.com/wizenheimer/airship/apps/api/internal/cloud/azure"
)

// azureSecret returns an Azure credential's application (client) ID: the
// half of it that is not a secret.
func azureSecret(secret []byte) (string, error) {
	parsed, err := azure.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return parsed.ClientID, nil
}
