package daytona

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// apiError is a response Daytona answered with an error status.
type apiError struct {
	op      string
	status  int
	message string
}

func (e *apiError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("%s: daytona answered %d", e.op, e.status)
	}

	return fmt.Sprintf("%s: %s (%d)", e.op, e.message, e.status)
}

// wrap turns a generated client's result into one error: an apiError when
// Daytona answered, the transport error otherwise, and closes the
// response. The generated clients read the body into the error's text as a
// status line only, so the message is taken from the body they hand back.
func wrap(op string, resp *http.Response, err error) error {
	if resp != nil && resp.Body != nil {
		// The generated clients have read the body and hand back a copy;
		// closing that copy is harmless and keeps the contract.
		defer resp.Body.Close()
	}
	if err == nil {
		return nil
	}
	if resp == nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if resp.StatusCode < http.StatusMultipleChoices {
		// Daytona answered, and the answer could not be read.
		return fmt.Errorf("%s: unreadable response: %w", op, err)
	}

	return &apiError{op: op, status: resp.StatusCode, message: bodyMessage(err, resp)}
}

type bodied interface{ Body() []byte }

func bodyMessage(err error, resp *http.Response) string {
	var body []byte
	var b bodied
	if errors.As(err, &b) {
		body = b.Body()
	}
	if len(body) == 0 && resp.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	}
	var parsed struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		switch m := parsed.Message.(type) {
		case string:
			if m != "" {
				return m
			}
		case []any:
			parts := make([]string, 0, len(m))
			for _, p := range m {
				parts = append(parts, fmt.Sprint(p))
			}
			if len(parts) > 0 {
				return strings.Join(parts, "; ")
			}
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 300 {
		msg = msg[:300]
	}

	return msg
}

func status(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.status
	}

	return 0
}

func isNotFound(err error) bool { return status(err) == http.StatusNotFound }

func isConflict(err error) bool { return status(err) == http.StatusConflict }

// isAuthError reports a key Daytona itself rejects: unknown, expired, or
// lacking the permission.
func isAuthError(err error) bool {
	switch status(err) {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	}

	return false
}

// isTransportError reports a call that got no answer: a timeout, a
// connection failure, a context that ended. The request may still have
// been received.
func isTransportError(err error) bool {
	if err == nil || status(err) != 0 {
		return false
	}
	var netErr net.Error

	return errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "EOF")
}

// isCapacityOrQuota reports failures that go away on their own: Daytona
// throttling or failing, or the organization at its quota. Daytona reports
// quota as a 4xx with a message, so the message is read.
func isCapacityOrQuota(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch {
	case apiErr.status == http.StatusTooManyRequests, apiErr.status == http.StatusRequestTimeout, apiErr.status >= http.StatusInternalServerError:
		return true
	case apiErr.status == http.StatusBadRequest, apiErr.status == http.StatusForbidden, apiErr.status == http.StatusConflict:
		msg := strings.ToLower(apiErr.message)
		// A sandbox larger than the organization's per-sandbox maximum
		// ("Disk request 20GB exceeds maximum allowed per sandbox (10GB)")
		// is a definition that can never launch, not a wait for capacity.
		// The organization's total ("Total memory limit exceeded. Maximum
		// allowed: 10GiB.") is a wait: other sandboxes end.
		for _, word := range []string{"per sandbox", "per-sandbox"} {
			if strings.Contains(msg, word) {
				return false
			}
		}
		for _, word := range []string{"quota", "limit exceeded", "exceeds", "capacity", "insufficient", "too many", "rate limit"} {
			if strings.Contains(msg, word) {
				return true
			}
		}
	}

	return false
}

// classify turns a provider failure into a cloud.LaunchError. Nothing from
// this provider is ambiguous: a sandbox that exists without a runner on it
// is useless, so a launch that cannot tell what happened cleans up and
// fails rather than leaving the inventory scan to revive an empty one.
func classify(err error) error {
	var launchErr *cloud.LaunchError
	if errors.As(err, &launchErr) {
		return err
	}
	if isTransportError(err) || isCapacityOrQuota(err) {
		return &cloud.LaunchError{Err: err, Retryable: true}
	}

	return &cloud.LaunchError{Err: err}
}

// errorMessage is Daytona's reason, without the request details.
func errorMessage(err error) string {
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.message != "" {
		return apiErr.message
	}

	return err.Error()
}
