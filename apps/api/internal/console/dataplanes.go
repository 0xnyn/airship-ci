package console

import (
	"context"
	"fmt"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agentapi"
	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// DataPlane is a customer-hosted agent that launches runners.
type DataPlane struct {
	ID           uuid.UUID  `doc:"Data plane ID"                                     format:"uuid" json:"id"`
	Name         string     `doc:"Name, unique in the organization"                  json:"name"`
	Status       string     `doc:"Whether the agent is connected"                    enum:"online,offline,pending" json:"status"`
	TokenHint    string     `doc:"First characters of the agent token"               json:"tokenHint"`
	AgentVersion *string    `doc:"Agent build last seen"                             json:"agentVersion"`
	Provider     *string    `doc:"Where the agent launches runners"                  json:"provider"`
	LastSeenAt   *time.Time `doc:"When the agent last synced"                        json:"lastSeenAt"`
	CreatedAt    time.Time  `doc:"When the data plane was created"                   json:"createdAt"`
}

func dataPlaneFromDB(d db.DataPlane, now time.Time) DataPlane {
	status := "pending"
	if d.LastSeenAt != nil {
		status = "offline"
		if now.Sub(*d.LastSeenAt) < onlineWindow {
			status = "online"
		}
	}

	return DataPlane{
		ID: d.ID, Name: d.Name, Status: status, TokenHint: d.TokenHint, AgentVersion: d.AgentVersion,
		Provider: d.AgentProvider, LastSeenAt: d.LastSeenAt, CreatedAt: d.CreatedAt,
	}
}

// DataPlaneList is every data plane in an organization.
type DataPlaneList struct {
	Items []DataPlane `json:"items"`
}

type dataPlaneListOutput struct {
	Body DataPlaneList
}

func (h *handlers) listDataPlanes(ctx context.Context, in *orgInput) (*dataPlaneListOutput, error) {
	inst, err := h.org(ctx, in.Org, false)
	if err != nil {
		return nil, err
	}
	rows, err := h.Store.ListDataPlanes(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("list data planes: %w", err)
	}
	now := time.Now()
	items := make([]DataPlane, 0, len(rows))
	for _, row := range rows {
		items = append(items, dataPlaneFromDB(row, now))
	}

	return &dataPlaneListOutput{Body: DataPlaneList{Items: items}}, nil
}

// CreateDataPlane is the request body for creating a data plane.
type CreateDataPlane struct {
	Name string `doc:"Name, unique in the organization" json:"name" maxLength:"63" minLength:"1" pattern:"^[a-z0-9][a-z0-9-]*$"`
}

type createDataPlaneInput struct {
	Org  string `maxLength:"100" minLength:"1" path:"org"`
	Body CreateDataPlane
}

// DataPlaneCredentials is a data plane and its agent token. The token is
// shown only in this response; Airship keeps just its hash.
type DataPlaneCredentials struct {
	DataPlane DataPlane `json:"dataPlane"`
	Token     string    `doc:"Agent token. Store it now: it cannot be shown again." json:"token"`
	// ControlPlaneURL is what the agent should connect to.
	ControlPlaneURL string `doc:"URL the agent connects to" json:"controlPlaneUrl"`
}

type credentialsOutput struct {
	Body DataPlaneCredentials
}

func newAgentToken() (token string, hint string) {
	token = auth.NewToken(agentapi.TokenPrefix)

	return token, token[:len(agentapi.TokenPrefix)+4]
}

func (h *handlers) createDataPlane(ctx context.Context, in *createDataPlaneInput) (*credentialsOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	token, hint := newAgentToken()
	row, err := h.Store.CreateDataPlane(ctx, db.CreateDataPlaneParams{
		InstallationID: inst.ID, Name: in.Body.Name, TokenHash: auth.HashToken(token), TokenHint: hint,
	})
	if isUniqueViolation(err) {
		return nil, huma.Error409Conflict("a data plane with that name already exists")
	}
	if err != nil {
		return nil, fmt.Errorf("create data plane: %w", err)
	}

	return &credentialsOutput{Body: DataPlaneCredentials{DataPlane: dataPlaneFromDB(row, time.Now()), Token: token, ControlPlaneURL: h.PublicURL}}, nil
}

func (h *handlers) rotateToken(ctx context.Context, in *orgIDInput) (*credentialsOutput, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	token, hint := newAgentToken()
	row, err := h.Store.RotateDataPlaneToken(ctx, db.RotateDataPlaneTokenParams{
		InstallationID: inst.ID, ID: in.ID, TokenHash: auth.HashToken(token), TokenHint: hint,
	})
	if isNoRows(err) {
		return nil, huma.Error404NotFound("data plane not found")
	}
	if err != nil {
		return nil, fmt.Errorf("rotate token: %w", err)
	}

	return &credentialsOutput{Body: DataPlaneCredentials{DataPlane: dataPlaneFromDB(row, time.Now()), Token: token, ControlPlaneURL: h.PublicURL}}, nil
}

func (h *handlers) deleteDataPlane(ctx context.Context, in *orgIDInput) (*struct{}, error) {
	inst, err := h.org(ctx, in.Org, true)
	if err != nil {
		return nil, err
	}
	if _, err := h.Store.GetDataPlane(ctx, db.GetDataPlaneParams{InstallationID: inst.ID, ID: in.ID}); err != nil {
		if isNoRows(err) {
			return nil, huma.Error404NotFound("data plane not found")
		}

		return nil, fmt.Errorf("get data plane: %w", err)
	}
	profiles, err := h.Store.CountDataPlaneProfiles(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("count profiles: %w", err)
	}
	if profiles > 0 {
		return nil, huma.Error409Conflict("delete or move this data plane's runner profiles first")
	}
	if _, err := h.Store.DeleteDataPlane(ctx, db.DeleteDataPlaneParams{InstallationID: inst.ID, ID: in.ID}); err != nil {
		return nil, fmt.Errorf("delete data plane: %w", err)
	}

	return &struct{}{}, nil
}
