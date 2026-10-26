package auth

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// account is the GitHub account an installation belongs to.
type account struct {
	InstallationID int64
	ID             int64
	Login          string
	Type           string // "Organization" or "User"
}

// resolveRole decides what a user may do with an installation: "admin",
// "member", or "" for no access. It returns an error only when GitHub could
// not answer, so callers can keep the previous decision instead of guessing.
//
// GitHub lists an installation to anyone who can reach even one of its
// repositories, including outside collaborators. Seeing an organization's
// jobs, runners and data planes takes active organization membership, and
// a personal account's installation is visible only to its owner.
func resolveRole(ctx context.Context, app *github.App, acct account, userID int64, userLogin string) (string, error) {
	if acct.Type == "User" {
		if acct.ID == userID {
			return roleAdmin, nil
		}

		return "", nil
	}
	if app == nil {
		return "", nil
	}
	role, err := app.Installation(acct.InstallationID).OrgRole(ctx, acct.Login, userLogin)
	if err != nil {
		return "", fmt.Errorf("organization role: %w", err)
	}
	switch role {
	case roleAdmin:
		return roleAdmin, nil
	case roleMember:
		return roleMember, nil
	default:
		return "", nil
	}
}

// RoleStore is what the role refresher reads and writes.
type RoleStore interface {
	ClaimStaleMemberships(ctx context.Context, arg db.ClaimStaleMembershipsParams) ([]db.ClaimStaleMembershipsRow, error)
	SetMembershipRole(ctx context.Context, arg db.SetMembershipRoleParams) error
	DeleteMembership(ctx context.Context, arg db.DeleteMembershipParams) error
}

// RoleRefresher re-checks dashboard roles with GitHub, so a demoted owner
// or someone who left the organization loses access within RefreshAfter
// rather than when their session expires.
type RoleRefresher struct {
	Store        RoleStore
	App          *github.App
	Logger       *slog.Logger
	RefreshAfter time.Duration
	Batch        int32
}

// Run refreshes every interval until ctx is done.
func (r *RoleRefresher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := r.Once(ctx); err != nil && ctx.Err() == nil {
			r.Logger.WarnContext(ctx, "refresh roles", slog.Any("error", err))
		}
	}
}

// Once refreshes one batch of memberships not checked within RefreshAfter.
func (r *RoleRefresher) Once(ctx context.Context) error {
	rows, err := r.Store.ClaimStaleMemberships(ctx, db.ClaimStaleMembershipsParams{
		Before:     time.Now().Add(-r.RefreshAfter),
		MaxResults: r.Batch,
	})
	if err != nil {
		return fmt.Errorf("claim memberships: %w", err)
	}
	for _, row := range rows {
		if row.GithubID == devGitHubID || row.DeletedAt != nil {
			continue
		}
		key := db.DeleteMembershipParams{UserID: row.UserID, InstallationID: row.InstallationID}
		role, err := resolveRole(ctx, r.App, account{InstallationID: row.InstallationID, ID: row.AccountID, Login: row.AccountLogin, Type: row.AccountType}, row.GithubID, row.Login)
		if err != nil {
			r.Logger.WarnContext(ctx, "could not refresh role", slog.String("org", row.AccountLogin), slog.String("user", row.Login), slog.Any("error", err))

			continue
		}
		switch {
		case role == "":
			r.Logger.InfoContext(ctx, "user lost access", slog.String("org", row.AccountLogin), slog.String("user", row.Login))
			if err := r.Store.DeleteMembership(ctx, key); err != nil {
				return fmt.Errorf("delete membership: %w", err)
			}
		case role != row.Role:
			r.Logger.InfoContext(ctx, "user role changed", slog.String("org", row.AccountLogin), slog.String("user", row.Login), slog.String("role", role))
			if err := r.Store.SetMembershipRole(ctx, db.SetMembershipRoleParams{UserID: row.UserID, InstallationID: row.InstallationID, Role: role}); err != nil {
				return fmt.Errorf("set role: %w", err)
			}
		}
	}

	return nil
}
