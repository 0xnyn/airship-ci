package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Profile maps one runs-on label to a machine shape on a data plane.
type Profile struct {
	ID               uuid.UUID `doc:"Profile ID"                                     format:"uuid" json:"id"`
	Label            string    `doc:"The runs-on label that selects this profile"    json:"label"`
	DataPlaneID      uuid.UUID `doc:"Data plane that launches the runners"           format:"uuid" json:"dataPlaneId"`
	Arch             string    `doc:"CPU architecture"                               enum:"x64,arm64" json:"arch"`
	InstanceType     string    `doc:"Machine type, such as c7i.xlarge"               json:"instanceType"`
	Image            *string   `doc:"Machine image; null uses the agent's default"   json:"image"`
	DiskGB           int32     `doc:"Root disk size in GiB"                          json:"diskGb"`
	Spot             bool      `doc:"Use spot capacity"                              json:"spot"`
	MaxRunners       int32     `doc:"Most runners of this profile at once"           json:"maxRunners"`
	AllowPublicRepos bool      `doc:"Run jobs from public repositories, including fork pull requests" json:"allowPublicRepos"`
	CreatedAt        time.Time `doc:"When the profile was created"                   json:"createdAt"`
	UpdatedAt        time.Time `doc:"When the profile last changed"                  json:"updatedAt"`
}

func profileFromDB(p db.Profile) Profile {
	return Profile{
		ID: p.ID, Label: p.Label, DataPlaneID: p.DataPlaneID, Arch: p.Arch, InstanceType: p.InstanceType,
		Image: p.Image, DiskGB: p.DiskGb, Spot: p.Spot, MaxRunners: p.MaxRunners,
		AllowPublicRepos: p.AllowPublicRepos, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// ProfileList is every profile in an organization.
type ProfileList struct {
	Items []Profile `json:"items"`
}

type profileListOutput struct {
	Body ProfileList
}

type profileOutput struct {
	Body Profile
}

func (h *handlers) listProfiles(ctx context.Context, in *orgInput) (*profileListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	rows, err := h.Store.ListProfiles(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	items := make([]Profile, 0, len(rows))
	for _, row := range rows {
		items = append(items, profileFromDB(row))
	}

	return &profileListOutput{Body: ProfileList{Items: items}}, nil
}

// CreateProfile is the request body for creating a runner profile.
type CreateProfile struct {
	Label            string    `doc:"runs-on label; lowercase letters, digits, '.', '_' and '-'" json:"label" maxLength:"63" minLength:"1" pattern:"^[a-z0-9][a-z0-9._-]*$"`
	DataPlaneID      uuid.UUID `doc:"Data plane that launches the runners" format:"uuid" json:"dataPlaneId"`
	Arch             string    `doc:"CPU architecture" enum:"x64,arm64" json:"arch"`
	InstanceType     string    `doc:"Machine type, such as c7i.xlarge" json:"instanceType" maxLength:"64" minLength:"1" pattern:"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"`
	Image            *string   `doc:"Machine image; omit to use the agent's default" json:"image,omitempty" maxLength:"256" minLength:"1"`
	DiskGB           int32     `default:"75" doc:"Root disk size in GiB" json:"diskGb,omitempty" maximum:"16384" minimum:"8"`
	Spot             bool      `default:"false" doc:"Use spot capacity" json:"spot,omitempty"`
	MaxRunners       int32     `default:"20" doc:"Most runners of this profile at once" json:"maxRunners,omitempty" maximum:"1000" minimum:"1"`
	AllowPublicRepos bool      `default:"false" doc:"Run jobs from public repositories, including fork pull requests" json:"allowPublicRepos,omitempty"`
}

type createProfileInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body CreateProfile
}

// reservedLabels are labels GitHub assigns or that name GitHub-hosted
// runners. A profile using one would either never match or steal jobs meant
// for GitHub's own runners.
var reservedLabels = map[string]bool{
	"self-hosted": true, "linux": true, "windows": true, "macos": true,
	"x64": true, "x86_64": true, "arm64": true, "arm": true,
}

var reservedLabelPrefixes = []string{"ubuntu-", "windows-", "macos-"}

func validateLabel(label string) error {
	if reservedLabels[label] {
		return huma.Error422UnprocessableEntity(fmt.Sprintf("%q is a label GitHub assigns itself", label))
	}
	for _, prefix := range reservedLabelPrefixes {
		if strings.HasPrefix(label, prefix) {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("labels starting with %q are used by GitHub-hosted runners", prefix))
		}
	}

	return nil
}

func (h *handlers) createProfile(ctx context.Context, in *createProfileInput) (*profileOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	if err := validateLabel(in.Body.Label); err != nil {
		return nil, err
	}
	if _, err := h.Store.GetDataPlane(ctx, db.GetDataPlaneParams{InstallationID: inst.ID, ID: in.Body.DataPlaneID}); err != nil {
		if isNoRows(err) {
			return nil, huma.Error422UnprocessableEntity("data plane not found in this organization")
		}

		return nil, fmt.Errorf("get data plane: %w", err)
	}
	row, err := h.Store.CreateProfile(ctx, db.CreateProfileParams{
		InstallationID: inst.ID, DataPlaneID: in.Body.DataPlaneID, Label: in.Body.Label, Arch: in.Body.Arch,
		InstanceType: in.Body.InstanceType, Image: in.Body.Image, DiskGb: in.Body.DiskGB, Spot: in.Body.Spot,
		MaxRunners: in.Body.MaxRunners, AllowPublicRepos: in.Body.AllowPublicRepos,
	})
	if isUniqueViolation(err) {
		return nil, huma.Error409Conflict("a profile with that label already exists")
	}
	if err != nil {
		return nil, fmt.Errorf("create profile: %w", err)
	}

	return &profileOutput{Body: profileFromDB(row)}, nil
}

// UpdateProfile is a partial profile update. Label, architecture and data
// plane are fixed: changing them would strand running jobs' runners.
type UpdateProfile struct {
	InstanceType     *string `doc:"New machine type" json:"instanceType,omitempty" maxLength:"64" minLength:"1" pattern:"^[a-zA-Z0-9][a-zA-Z0-9._-]*$"`
	Image            *string `doc:"New machine image; an empty string restores the agent's default" json:"image,omitempty" maxLength:"256"`
	DiskGB           *int32  `doc:"New root disk size in GiB" json:"diskGb,omitempty" maximum:"16384" minimum:"8"`
	Spot             *bool   `doc:"Use spot capacity" json:"spot,omitempty"`
	MaxRunners       *int32  `doc:"Most runners at once" json:"maxRunners,omitempty" maximum:"1000" minimum:"1"`
	AllowPublicRepos *bool   `doc:"Run jobs from public repositories" json:"allowPublicRepos,omitempty"`
}

type updateProfileInput struct {
	Org  string    `maxLength:"100" minLength:"1" path:"org"`
	ID   uuid.UUID `format:"uuid" path:"id"`
	Body UpdateProfile
}

func (h *handlers) updateProfile(ctx context.Context, in *updateProfileInput) (*profileOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	params := db.UpdateProfileParams{
		InstallationID: inst.ID, ID: in.ID, InstanceType: in.Body.InstanceType, DiskGb: in.Body.DiskGB,
		Spot: in.Body.Spot, MaxRunners: in.Body.MaxRunners, AllowPublicRepos: in.Body.AllowPublicRepos,
	}
	if in.Body.Image != nil {
		params.SetImage = true
		if *in.Body.Image != "" {
			params.Image = in.Body.Image
		}
	}
	row, err := h.Store.UpdateProfile(ctx, params)
	if isNoRows(err) {
		return nil, huma.Error404NotFound("profile not found")
	}
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}

	return &profileOutput{Body: profileFromDB(row)}, nil
}

func (h *handlers) deleteProfile(ctx context.Context, in *orgIDInput) (*struct{}, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	err = h.Fleet.DeleteProfile(ctx, inst.ID, in.ID)
	var busy *fleet.ProfileBusyError
	switch {
	case errors.Is(err, fleet.ErrProfileNotFound):
		return nil, huma.Error404NotFound("profile not found")
	case errors.As(err, &busy):
		return nil, huma.Error409Conflict(fmt.Sprintf("%d runners of this profile are still active; lower max runners to stop new ones and try again once they finish", busy.Active))
	case err != nil:
		return nil, fmt.Errorf("delete profile: %w", err)
	}

	return &struct{}{}, nil
}
