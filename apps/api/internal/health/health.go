// Package health serves the readiness endpoint used by load balancers and
// the dashboard's status indicator.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

const pingTimeout = 2 * time.Second

// Pinger checks that a dependency, such as the database, is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthStatus reports whether the api and its dependencies are up.
type HealthStatus struct {
	Status   string `doc:"Overall status"   enum:"ok,degraded" json:"status"`
	Database string `doc:"Database status"  enum:"ok,down"     json:"database"`
	Version  string `doc:"Running api build"                   json:"version"`
}

type output struct {
	Body HealthStatus
}

// Register adds GET /healthz to api.
func Register(api huma.API, db Pinger, version string) {
	huma.Register(api, huma.Operation{
		OperationID: "getHealth",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Check service health",
		Tags:        []string{"Health"},
	}, func(ctx context.Context, _ *struct{}) (*output, error) {
		status := HealthStatus{Status: "ok", Database: "ok", Version: version}

		ctx, cancel := context.WithTimeout(ctx, pingTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			status.Status = "degraded"
			status.Database = "down"
		}

		return &output{Body: status}, nil
	})
}
