package gcp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/googleapi"
)

// Operation error codes Compute Engine reports when an insert fails after
// it was accepted.
const (
	codeZoneExhausted            = "ZONE_RESOURCE_POOL_EXHAUSTED"
	codeZoneExhaustedWithDetails = "ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS"
	codeQuotaExceeded            = "QUOTA_EXCEEDED"
	codeRateLimitExceeded        = "RATE_LIMIT_EXCEEDED"
	codeResourceNotReady         = "RESOURCE_NOT_READY"
	codeInternalError            = "INTERNAL_ERROR"
	// codeResourceInUse: an existing disk is attached read-write to
	// another runner; it frees when that runner finishes.
	codeResourceInUse = "RESOURCE_IN_USE_BY_ANOTHER_RESOURCE"
)

// apiError returns the API error inside err, if there is one.
func apiError(err error) *googleapi.Error {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr
	}

	return nil
}

// httpStatus is the HTTP status of an API error, or 0 for anything else.
func httpStatus(err error) int {
	if apiErr := apiError(err); apiErr != nil {
		return apiErr.Code
	}

	return 0
}

func isNotFound(err error) bool { return httpStatus(err) == http.StatusNotFound }

func isConflict(err error) bool { return httpStatus(err) == http.StatusConflict }

// reasons collects the reason strings of an API error (notFound,
// quotaExceeded, rateLimitExceeded, ...).
func reasons(err error) []string {
	apiErr := apiError(err)
	if apiErr == nil {
		return nil
	}
	out := make([]string, 0, len(apiErr.Errors))
	for _, item := range apiErr.Errors {
		out = append(out, item.Reason)
	}

	return out
}

func hasReason(err error, want ...string) bool {
	for _, r := range reasons(err) {
		for _, w := range want {
			if r == w {
				return true
			}
		}
	}

	return false
}

// isTransportError reports a call that got no answer from Compute Engine:
// a timeout, a connection failure or a context that ended. Such a call may
// still have been received.
func isTransportError(err error) bool {
	if err == nil || apiError(err) != nil {
		return false
	}
	var netErr net.Error

	return errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "EOF")
}

// operationErrorCodes lists the error codes of a finished operation.
func operationErrorCodes(op *computepb.Operation) []string {
	if op == nil || op.GetError() == nil {
		return nil
	}
	out := make([]string, 0, len(op.GetError().GetErrors()))
	for _, e := range op.GetError().GetErrors() {
		out = append(out, e.GetCode())
	}

	return out
}

// operationError is an insert that Compute Engine accepted and then
// failed, with the codes it gave.
type operationError struct {
	codes   []string
	message string
}

func (e *operationError) Error() string { return e.message }

func (e *operationError) has(code string) bool {
	for _, c := range e.codes {
		if c == code {
			return true
		}
	}

	return false
}

// isZoneSpecific reports failures another zone of the region may not have:
// the zone is out of the machine type or accelerator, or does not offer it
// at all.
func isZoneSpecific(err error, zone string) bool {
	var opErr *operationError
	if errors.As(err, &opErr) {
		return opErr.has(codeZoneExhausted) || opErr.has(codeZoneExhaustedWithDetails)
	}
	if apiErr := apiError(err); apiErr != nil && (apiErr.Code == http.StatusBadRequest || apiErr.Code == http.StatusNotFound) {
		return strings.Contains(apiErr.Message, "zones/"+zone+"/") || strings.Contains(apiErr.Body, "zones/"+zone+"/")
	}

	return false
}

// isCapacityOrQuota reports failures that go away on their own: no
// capacity, quota or rate limits, the service itself.
func isCapacityOrQuota(err error) bool {
	var opErr *operationError
	if errors.As(err, &opErr) {
		for _, code := range []string{codeZoneExhausted, codeZoneExhaustedWithDetails, codeQuotaExceeded, codeRateLimitExceeded, codeResourceNotReady, codeInternalError, codeResourceInUse} {
			if opErr.has(code) {
				return true
			}
		}

		return false
	}
	switch status := httpStatus(err); {
	case status == http.StatusTooManyRequests, status >= http.StatusInternalServerError:
		return true
	case status == http.StatusForbidden:
		return hasReason(err, "quotaExceeded", "rateLimitExceeded", "userRateLimitExceeded")
	}

	return false
}

// classify turns a provider failure into a cloud.LaunchError. Capacity,
// quota, throttling and transport failures are retryable; everything
// Compute Engine refused is permanent and moves the job on to its next
// fallback.
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

// isAuthError reports a credential Compute Engine itself rejects, or one
// whose project has the API turned off.
func isAuthError(err error) bool {
	switch httpStatus(err) {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}

	return false
}

// errorMessage is the cloud's reason, without the request details.
func errorMessage(err error) string {
	if apiErr := apiError(err); apiErr != nil {
		msg := apiErr.Message
		if msg == "" && len(apiErr.Errors) > 0 {
			msg = apiErr.Errors[0].Message
		}
		if strings.Contains(msg, "has not been used in project") || strings.Contains(msg, "is disabled") {
			msg += "; enable the Compute Engine API for the project"
		}

		return msg
	}

	return err.Error()
}
