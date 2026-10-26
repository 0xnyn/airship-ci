package fleet

import (
	"context"
	"errors"
	"fmt"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/google/uuid"
)

// ErrCloudInUse is returned when credentials with live runners would be
// removed.
var ErrCloudInUse = errors.New("runners are still using these credentials")

// ErrCloudNotFound is returned for a cloud with no credentials.
var ErrCloudNotFound = errors.New("no credentials for this cloud")

// ConnectCloud verifies credentials with the cloud, seals them and stores
// them for the installation, replacing any it had for that cloud. by is
// the user who did it; nil for a developer API token.
func (s *Service) ConnectCloud(ctx context.Context, installationID int64, cloudName string, secret []byte, defaultRegion string, by *uuid.UUID) (db.CloudCredential, error) {
	provider, err := s.clouds.Get(cloudName)
	if err != nil {
		return db.CloudCredential{}, err
	}
	identity, err := provider.Verify(ctx, secret, defaultRegion)
	if err != nil {
		return db.CloudCredential{}, err //nolint:wrapcheck // the provider's error names the cloud's reason
	}
	sealed, err := s.secrets.Seal(secret)
	if err != nil {
		return db.CloudCredential{}, fmt.Errorf("seal credentials: %w", err)
	}
	row, err := s.q.UpsertCloudCredential(ctx, db.UpsertCloudCredentialParams{
		InstallationID: installationID, Cloud: cloudName, Secret: sealed, Identity: identityHint(cloudName, secret),
		AccountID: nilIfEmpty(identity.AccountID), Principal: nilIfEmpty(identity.Principal),
		DefaultRegion: defaultRegion, CreatedBy: by,
	})
	if err != nil {
		return db.CloudCredential{}, fmt.Errorf("save credentials: %w", err)
	}
	s.record(ctx, installationID, "cloud.connected", levelInfo,
		fmt.Sprintf("Connected %s as %s (default region %s)", cloudName, identity.Principal, defaultRegion), eventFields{})
	// Jobs skipped for want of a cloud are retried by the stale job
	// refresh; pools can start now.
	s.fillInstallationPools(ctx, installationID)

	return row, nil
}

// DisconnectCloud removes an installation's credentials for a cloud. It
// refuses while runners launched with them are alive, because without the
// secret their instances could not be terminated.
func (s *Service) DisconnectCloud(ctx context.Context, installationID int64, cloudName string) error {
	cred, err := s.q.GetCloudCredential(ctx, db.GetCloudCredentialParams{InstallationID: installationID, Cloud: cloudName})
	if isNoRows(err) {
		return ErrCloudNotFound
	}
	if err != nil {
		return fmt.Errorf("get credential: %w", err)
	}
	active, err := s.q.CountActiveRunnersForCredential(ctx, &cred.ID)
	if err != nil {
		return fmt.Errorf("count runners: %w", err)
	}
	if active > 0 {
		return fmt.Errorf("%w: %d", ErrCloudInUse, active)
	}
	if _, err := s.q.DeleteCloudCredential(ctx, db.DeleteCloudCredentialParams{InstallationID: installationID, Cloud: cloudName}); err != nil {
		return fmt.Errorf("delete credential: %w", err)
	}
	s.record(ctx, installationID, "cloud.disconnected", levelInfo, "Disconnected "+cloudName, eventFields{})

	return nil
}

// identityHint is the public half of a secret, shown in the dashboard.
func identityHint(cloudName string, secret []byte) string {
	if cloudName == cloud.AWS {
		if parsed, err := awsSecret(secret); err == nil {
			return parsed
		}
	}

	return "connected"
}
