package fleet

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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
	// Rotating a key within the account is fine: the new key can still
	// terminate the instances the old one launched. Another account
	// cannot, so live runners keep the credential they were launched with.
	existing, err := s.q.GetCloudCredential(ctx, db.GetCloudCredentialParams{InstallationID: installationID, Cloud: cloudName})
	if err != nil && !isNoRows(err) {
		return db.CloudCredential{}, fmt.Errorf("get credential: %w", err)
	}
	if err == nil && identity.AccountID != "" && existing.AccountID != nil && *existing.AccountID != identity.AccountID {
		active, err := s.q.CountActiveRunnersForCredential(ctx, &existing.ID)
		if err != nil {
			return db.CloudCredential{}, fmt.Errorf("count runners: %w", err)
		}
		if active > 0 {
			return db.CloudCredential{}, fmt.Errorf("%w: %d runners launched in account %s are still alive", ErrCloudInUse, active, *existing.AccountID)
		}
	}
	sealed, err := s.secrets.Seal(secret, credentialContext(installationID, cloudName))
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
	// Jobs skipped for want of a cloud can be planned now, and pools can
	// start.
	s.retrySkippedJobs(ctx, installationID, nil, SkipNoCloud)
	s.fillInstallationPools(ctx, installationID)

	return row, nil
}

// credentialContext binds a sealed secret to its cloud_credentials row.
func credentialContext(installationID int64, cloudName string) string {
	return "credential:" + strconv.FormatInt(installationID, 10) + ":" + cloudName
}

// DisconnectCloud removes an installation's credentials for a cloud. It
// refuses while runners launched with them are alive, because without the
// secret their instances could not be terminated. The installation is
// locked while counting, as every path that creates a runner locks it
// too, so no runner can appear between the count and the delete.
func (s *Service) DisconnectCloud(ctx context.Context, installationID int64, cloudName string) error {
	err := s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockInstallation(ctx, installationID); err != nil {
			return fmt.Errorf("lock installation: %w", err)
		}
		cred, err := q.GetCloudCredential(ctx, db.GetCloudCredentialParams{InstallationID: installationID, Cloud: cloudName})
		if isNoRows(err) {
			return ErrCloudNotFound
		}
		if err != nil {
			return fmt.Errorf("get credential: %w", err)
		}
		active, err := q.CountActiveRunnersForCredential(ctx, &cred.ID)
		if err != nil {
			return fmt.Errorf("count runners: %w", err)
		}
		if active > 0 {
			return fmt.Errorf("%w: %d", ErrCloudInUse, active)
		}
		if _, err := q.DeleteCloudCredential(ctx, db.DeleteCloudCredentialParams{InstallationID: installationID, Cloud: cloudName}); err != nil {
			return fmt.Errorf("delete credential: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}
	s.record(ctx, installationID, "cloud.disconnected", levelInfo, "Disconnected "+cloudName, eventFields{})

	return nil
}

// identityHint is the public half of a secret, shown in the dashboard.
func identityHint(cloudName string, secret []byte) string {
	var hint string
	var err error
	switch cloudName {
	case cloud.AWS:
		hint, err = awsSecret(secret)
	case cloud.GCP:
		hint, err = gcpSecret(secret)
	case cloud.Azure:
		hint, err = azureSecret(secret)
	case cloud.Daytona:
		hint, err = daytonaSecret(secret)
	case cloud.Runloop:
		hint, err = runloopSecret(secret)
	case cloud.E2B, cloud.Novita, cloud.Lelantos:
		hint, err = e2bSecret(cloudName, secret)
	case cloud.Namespace:
		hint, err = namespaceSecret(secret)
	case cloud.Tenki:
		hint, err = tenkiSecret(secret)
	case cloud.Modal:
		hint, err = modalSecret(secret)
	default:
		return "connected"
	}
	if err != nil {
		return "connected"
	}

	return hint
}
