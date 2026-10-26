// Package webhook receives GitHub App webhooks.
//
// It is plain net/http rather than a Huma operation because the signature
// covers the exact request bytes, which must be read before any decoding.
package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/wizenheimer/airship/apps/api/internal/github"
)

// Path is where GitHub delivers webhooks.
const Path = "/v1/github/webhook"

// maxPayloadBytes is GitHub's own cap on webhook payloads.
const maxPayloadBytes = 25 << 20

// Handler is what the webhook dispatches to.
type Handler interface {
	HandleWorkflowJob(ctx context.Context, event github.WorkflowJobEvent) error
	HandleInstallation(ctx context.Context, event github.InstallationEvent) error
	HandlePush(ctx context.Context, event github.PushEvent) error
	HandleRepository(ctx context.Context, event github.RepositoryEvent) error
}

// New returns the webhook endpoint. Requests with a missing or wrong
// signature are rejected before their body is parsed.
func New(secret []byte, handler Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPayloadBytes))
		if err != nil {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)

			return
		}
		if !github.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)

			return
		}

		event := r.Header.Get("X-GitHub-Event")
		delivery := r.Header.Get("X-GitHub-Delivery")
		log := logger.With(slog.String("event", event), slog.String("delivery", delivery))

		err = dispatch(r.Context(), handler, event, body)
		switch {
		case errors.Is(err, errIgnored):
			w.WriteHeader(http.StatusAccepted)
		case errors.Is(err, errMalformed):
			log.WarnContext(r.Context(), "malformed webhook payload")
			http.Error(w, "malformed payload", http.StatusBadRequest)
		case err != nil:
			// A 5xx marks the delivery failed, so the redeliverer replays it.
			log.ErrorContext(r.Context(), "handle webhook", slog.Any("error", err))
			http.Error(w, "could not process webhook", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
}

var (
	errIgnored   = errors.New("event ignored")
	errMalformed = errors.New("malformed payload")
)

func dispatch(ctx context.Context, handler Handler, event string, body []byte) error {
	switch event {
	case "workflow_job":
		var payload github.WorkflowJobEvent
		if err := json.Unmarshal(body, &payload); err != nil {
			return errMalformed
		}

		return handler.HandleWorkflowJob(ctx, payload) //nolint:wrapcheck // logged with context above
	case "installation":
		var payload github.InstallationEvent
		if err := json.Unmarshal(body, &payload); err != nil || payload.Installation.ID == 0 {
			return errMalformed
		}

		return handler.HandleInstallation(ctx, payload) //nolint:wrapcheck // logged with context above
	case "push":
		var payload github.PushEvent
		if err := json.Unmarshal(body, &payload); err != nil {
			return errMalformed
		}

		return handler.HandlePush(ctx, payload) //nolint:wrapcheck // logged with context above
	case "repository":
		var payload github.RepositoryEvent
		if err := json.Unmarshal(body, &payload); err != nil {
			return errMalformed
		}

		return handler.HandleRepository(ctx, payload) //nolint:wrapcheck // logged with context above
	default:
		// ping, installation_repositories and anything else Airship does not
		// act on.
		return errIgnored
	}
}
