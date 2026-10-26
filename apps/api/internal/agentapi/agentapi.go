// Package agentapi serves the data plane agent protocol. Agents
// authenticate with their data plane's bearer token, and every endpoint is
// scoped to that one data plane.
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/auth"
	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
)

// TokenPrefix starts every data plane token, so leaked tokens are easy to
// recognize in logs and by secret scanners.
const TokenPrefix = "asd_"

// maxSyncBytes fits a full inventory of 10,000 instances.
const maxSyncBytes = 4 << 20

// Store is what authentication needs.
type Store interface {
	AuthenticateDataPlane(ctx context.Context, tokenHash []byte) (db.AuthenticateDataPlaneRow, error)
}

// Syncer runs one sync round.
type Syncer interface {
	Sync(ctx context.Context, dp fleet.DataPlane, req agentproto.SyncRequest) (agentproto.SyncResponse, error)
}

type syncInput struct {
	Authorization string `doc:"Bearer data plane token" header:"Authorization" required:"true"`
	Body          agentproto.SyncRequest
}

type syncOutput struct {
	Body agentproto.SyncResponse
}

// Register adds the agent endpoints.
func Register(api huma.API, store Store, syncer Syncer) {
	huma.Register(api, huma.Operation{
		OperationID:  "agentSync",
		Method:       http.MethodPost,
		Path:         agentproto.SyncPath,
		Summary:      "Sync a data plane agent",
		Description:  "Reports the agent's inventory and returns runners to launch and instances to terminate. Long-polls up to waitSeconds when there is nothing to do.",
		Tags:         []string{"Agent"},
		MaxBodyBytes: maxSyncBytes,
	}, func(ctx context.Context, in *syncInput) (*syncOutput, error) {
		dp, err := authenticate(ctx, store, in.Authorization)
		if err != nil {
			return nil, err
		}
		resp, err := syncer.Sync(ctx, dp, in.Body)
		if err != nil {
			return nil, fmt.Errorf("sync: %w", err)
		}

		return &syncOutput{Body: resp}, nil
	})
}

func authenticate(ctx context.Context, store Store, header string) (fleet.DataPlane, error) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || !strings.HasPrefix(token, TokenPrefix) {
		return fleet.DataPlane{}, huma.Error401Unauthorized("missing data plane token")
	}
	row, err := store.AuthenticateDataPlane(ctx, auth.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return fleet.DataPlane{}, huma.Error401Unauthorized("invalid data plane token")
	}
	if err != nil {
		return fleet.DataPlane{}, fmt.Errorf("authenticate data plane: %w", err)
	}

	return fleet.DataPlane{
		DataPlane: row.DataPlane,
		Retired:   row.InstallationDeletedAt != nil,
		Paused:    row.InstallationSuspendedAt != nil,
	}, nil
}
