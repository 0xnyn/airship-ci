package fleet

import (
	awscloud "github.com/wizenheimer/airship/apps/api/internal/cloud/aws"
)

// awsSecret returns an AWS secret's access key ID.
func awsSecret(secret []byte) (string, error) {
	parsed, err := awscloud.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return parsed.AccessKeyID, nil
}
