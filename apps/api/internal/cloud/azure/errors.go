package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// Error codes Azure reports, by what the lifecycle does about them.
var (
	// retryableCodes go away on their own or as other machines finish: no
	// capacity for the size, quota, throttling, a resource busy with
	// another operation, or the service itself.
	retryableCodes = []string{
		"AllocationFailed", "ZonalAllocationFailed", "OverconstrainedAllocationRequest", "OverconstrainedZonalAllocationRequest",
		"QuotaExceeded", "PublicIPCountLimitReached", "TooManyRequests", "RetryableError", "RetryLater",
		"AnotherOperationInProgress", "ConflictingUserInput", "CanceledAndSupersededDueToAnotherOperation",
		"InternalExecutionError", "InternalOperationError", "InternalError", "OperationPreempted", "VMPlacementFailed",
		"ResourceGroupBeingDeleted", "TargetDiskBlobAlreadyExists", "ServiceUnavailable", "GatewayTimeout", "RequestTimeout",
		// The machine allocated but its agent never reported: the next one
		// usually does.
		"OSProvisioningTimedOut", "VMAgentStatusCommunicationError", "VMExtensionProvisioningTimeout",
	}
	// notFoundCodes mean the resource is not there.
	notFoundCodes = []string{"ResourceNotFound", "NotFound", "ResourceGroupNotFound", "ParentResourceNotFound", "SubscriptionNotFound"}
	// authCodes mean the credential itself was refused.
	authCodes = []string{"AuthorizationFailed", "InvalidAuthenticationToken", "InvalidAuthenticationTokenTenant", "ExpiredAuthenticationToken", "AuthenticationFailed"}
	// marketplaceCodes mean the image needs its terms accepted, which only
	// the customer can do.
	marketplaceCodes = []string{"MarketplacePurchaseEligibilityFailed", "ResourcePurchaseValidationFailed"}
)

var codePattern = regexp.MustCompile(`"code"\s*:\s*"([A-Za-z0-9_.]+)"`)

// responseError returns the ARM error inside err, if there is one.
func responseError(err error) *azcore.ResponseError {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr
	}

	return nil
}

// httpStatus is the HTTP status of an ARM error, or 0 for anything else.
func httpStatus(err error) int {
	if respErr := responseError(err); respErr != nil {
		return respErr.StatusCode
	}

	return 0
}

// codes lists every error code in an ARM error: the top-level one and the
// ones nested in details, which is where the reason a deployment failed
// often is.
func codes(err error) []string {
	respErr := responseError(err)
	if respErr == nil {
		return nil
	}
	out := []string{}
	if respErr.ErrorCode != "" {
		out = append(out, respErr.ErrorCode)
	}
	for _, m := range codePattern.FindAllStringSubmatch(respErr.Error(), -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}

	return out
}

func hasCode(err error, want ...string) bool {
	for _, code := range codes(err) {
		if slices.Contains(want, code) {
			return true
		}
	}

	return false
}

// isNotFound reports a resource that is not there. The status code is the
// answer for a direct read; a code is what a failed operation says.
func isNotFound(err error) bool {
	return httpStatus(err) == http.StatusNotFound || hasCode(err, notFoundCodes...)
}

// isTransportError reports a call that got no answer from Azure: a
// timeout, a connection failure or a context that ended. Such a call may
// still have been received.
func isTransportError(err error) bool {
	if err == nil || responseError(err) != nil {
		return false
	}
	var netErr net.Error

	return errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "EOF")
}

// isQuota reports a refusal for want of quota. Azure says so with
// OperationNotAllowed and a message about the quota, or QuotaExceeded.
func isQuota(err error) bool {
	if hasCode(err, "QuotaExceeded") {
		return true
	}

	return hasCode(err, "OperationNotAllowed") && strings.Contains(strings.ToLower(err.Error()), "quota")
}

// isCapacityOrQuota reports failures that go away on their own: no
// capacity, quota or rate limits, the service itself.
func isCapacityOrQuota(err error) bool {
	if isQuota(err) || hasCode(err, retryableCodes...) {
		return true
	}
	switch status := httpStatus(err); {
	case status == http.StatusTooManyRequests, status == http.StatusRequestTimeout:
		return true
	case status >= http.StatusInternalServerError && status != http.StatusNotImplemented:
		// A failed operation is read back with status 200 and carries its
		// code instead; a 5xx here is the API itself failing.
		return true
	}

	return false
}

// classify turns a provider failure into a cloud.LaunchError. Capacity,
// quota, throttling and transport failures are retryable; everything Azure
// refused is permanent and moves the job on to its next fallback.
func classify(err error) error {
	var launchErr *cloud.LaunchError
	if errors.As(err, &launchErr) {
		return err
	}

	return &cloud.LaunchError{Err: err, Retryable: retryable(err)}
}

// retryable reports a failure worth trying again as is.
func retryable(err error) bool {
	return isTransportError(err) || isCapacityOrQuota(err)
}

// refused is the LaunchError for a call Azure answered with a failure:
// classified by the answer's code, described by its message alone (the
// SDK's error text carries the whole request and response).
func refused(err error, format string, args ...any) error {
	return &cloud.LaunchError{
		Err:       fmt.Errorf(format+": %s", append(args, errorMessage(err))...),
		Retryable: retryable(err),
	}
}

// isAuthError reports a credential Azure itself rejects: a secret Entra
// does not accept, a token ARM refuses, or a subscription the application
// cannot see (which ARM reports as not found).
func isAuthError(err error) bool {
	var authErr *azidentity.AuthenticationFailedError
	if errors.As(err, &authErr) {
		return true
	}
	switch httpStatus(err) {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}

	return hasCode(err, authCodes...)
}

// errorMessage is the cloud's reason, without the request details the
// SDK's message carries (the URL, headers and the whole body).
func errorMessage(err error) string {
	respErr := responseError(err)
	if respErr == nil {
		var authErr *azidentity.AuthenticationFailedError
		if errors.As(err, &authErr) {
			return "Microsoft Entra did not accept the client secret"
		}

		return err.Error()
	}
	msg := bodyMessage(respErr.Error())
	if msg == "" {
		msg = respErr.ErrorCode
	}
	if msg == "" {
		msg = http.StatusText(respErr.StatusCode)
	}
	if hasCode(err, marketplaceCodes...) {
		msg += "; accept the image's marketplace terms for the subscription (az vm image terms accept)"
	}

	return msg
}

var messagePattern = regexp.MustCompile(`"message"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// bodyMessage is the first message in an ARM error body.
func bodyMessage(text string) string {
	m := messagePattern.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	msg := strings.ReplaceAll(m[1], `\"`, `"`)
	msg = strings.ReplaceAll(msg, `\n`, " ")
	if len(msg) > 500 {
		msg = msg[:500]
	}

	return msg
}
