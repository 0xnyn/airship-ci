package fleet

import (
	"context"
	"log/slog"

	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/google/uuid"
)

// Event levels, mirroring events.level.
const (
	levelInfo    = "info"
	levelWarning = "warning"
	levelError   = "error"
)

type eventFields struct {
	repository string
	runnerID   *uuid.UUID
	jobID      *int64
}

// record writes a dashboard event. It is best effort: a failure to record
// what happened must not undo what happened.
func (s *Service) record(ctx context.Context, installationID int64, kind, level, message string, fields eventFields) {
	err := s.q.CreateEvent(context.WithoutCancel(ctx), db.CreateEventParams{
		InstallationID: installationID, Kind: kind, Level: level, Message: truncate(message, 2000),
		Repository: nilIfEmpty(fields.repository), RunnerID: fields.runnerID, JobID: fields.jobID,
	})
	if err != nil {
		s.log.WarnContext(ctx, "record event", slog.String("kind", kind), slog.Any("error", err))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}
