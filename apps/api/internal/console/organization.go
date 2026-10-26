package console

import (
	"context"
	"fmt"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/google/uuid"
)

type uuidPtr = uuid.UUID

// Member is a GitHub organization member who can open the dashboard.
type Member struct {
	Login       string    `doc:"GitHub login"                                   json:"login"`
	Name        *string   `doc:"Display name"                                   json:"name"`
	AvatarURL   *string   `doc:"Avatar image URL"                               json:"avatarUrl"`
	Role        string    `doc:"Dashboard role, from the GitHub organization role" enum:"admin,member" json:"role"`
	RefreshedAt time.Time `doc:"When the role was last checked with GitHub"     json:"refreshedAt"`
}

// MemberList is everyone who has signed in to the organization's dashboard.
type MemberList struct {
	Items []Member `json:"items"`
}

type memberListOutput struct {
	Body MemberList
}

func (h *handlers) listMembers(ctx context.Context, in *orgInput) (*memberListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	rows, err := h.Store.ListInstallationMembers(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	items := make([]Member, 0, len(rows))
	for _, row := range rows {
		items = append(items, Member{Login: row.User.Login, Name: row.User.Name, AvatarURL: row.User.AvatarUrl, Role: row.Role, RefreshedAt: row.RefreshedAt})
	}

	return &memberListOutput{Body: MemberList{Items: items}}, nil
}

// Settings are an organization's tunables.
type Settings struct {
	MaxRunners int32 `doc:"Most live runners at once across every repository" json:"maxRunners" maximum:"10000" minimum:"1"`
}

type settingsOutput struct {
	Body Settings
}

func (h *handlers) getSettings(ctx context.Context, in *orgInput) (*settingsOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}

	return &settingsOutput{Body: Settings{MaxRunners: inst.MaxRunners}}, nil
}

type updateSettingsInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body Settings
}

func (h *handlers) updateSettings(ctx context.Context, in *updateSettingsInput) (*settingsOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	row, err := h.Store.SetInstallationMaxRunners(ctx, db.SetInstallationMaxRunnersParams{ID: inst.ID, MaxRunners: in.Body.MaxRunners})
	if err != nil {
		return nil, fmt.Errorf("update settings: %w", err)
	}

	return &settingsOutput{Body: Settings{MaxRunners: row.MaxRunners}}, nil
}
