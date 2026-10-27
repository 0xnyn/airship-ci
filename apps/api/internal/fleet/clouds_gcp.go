package fleet

import (
	"github.com/wizenheimer/airship/apps/api/internal/cloud/gcp"
	"github.com/wizenheimer/airship/apps/api/internal/db"
)

// gcpSecret returns a GCP service account key's client email.
func gcpSecret(secret []byte) (string, error) {
	parsed, err := gcp.ParseSecret(secret)
	if err != nil {
		return "", err //nolint:wrapcheck // caller only tests for success
	}

	return parsed.ClientEmail, nil
}

// gcpScope turns a GCP location into the region the runner is recorded in
// and the zone its spec pins, if the location (from the config or the
// credential's default) was a zone. The region carries the project when
// extra_config.project_id overrides the credential's, so the inventory
// scan lists the right project and runners of different projects never
// share a scope.
func gcpScope(location string, spec Spec, cred db.CloudCredential) (string, Spec) {
	region, zone, ok := gcp.SplitLocation(location)
	if !ok {
		// Not a location at all; the launch fails and says so.
		return location, spec
	}
	if zone != "" && spec.Zone == "" {
		spec.Zone = zone
	}
	credentialProject := ""
	if cred.AccountID != nil {
		credentialProject = *cred.AccountID
	}
	project := credentialProject
	if spec.GCP != nil && spec.GCP.Project != "" {
		project = spec.GCP.Project
	}

	return gcp.EncodeScope(region, project, credentialProject), spec
}
