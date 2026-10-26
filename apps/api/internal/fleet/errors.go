package fleet

import (
	"errors"
	"net/http"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/github"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// isRetryableTx reports deadlocks and serialization failures.
func isRetryableTx(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" || pgErr.Code == "40001"
	}

	return false
}

// isForeignKeyViolation reports a write that referenced a row deleted
// concurrently, such as a profile deleted while a job was being matched.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

const (
	// transientBackoff spaces out retries after errors that carry no hint.
	transientBackoff = 5 * time.Second
	// maxBackoff caps how long a rate limit can hold a runner back.
	maxBackoff = 15 * time.Minute
)

// backoffFor is how long to wait before retrying after err: what GitHub
// asked for with Retry-After or a rate limit reset, or a short default.
func backoffFor(err error) time.Duration {
	var apiErr *github.APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return min(apiErr.RetryAfter, maxBackoff)
	}

	return transientBackoff
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// isPermanent reports whether a GitHub error will not go away on retry:
// the app lost access, the repository is gone, or the request is invalid.
func isPermanent(err error) bool {
	switch github.StatusOf(err) {
	case http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusGone:
		return true
	case http.StatusForbidden:
		// 403 is also how GitHub reports rate limits; those carry Retry-After.
		var apiErr *github.APIError
		if errors.As(err, &apiErr) {
			return apiErr.RetryAfter == 0
		}

		return true
	default:
		return false
	}
}
