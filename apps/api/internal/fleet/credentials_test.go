package fleet_test

import (
	"errors"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	awscloud "github.com/wizenheimer/airship/apps/api/internal/cloud/aws"
)

func (h *harness) credentialStatus() (string, string) {
	h.t.Helper()
	var status, reason string
	if err := h.pool.QueryRow(h.ctx, "select status, coalesce(error, '') from cloud_credentials").Scan(&status, &reason); err != nil {
		h.t.Fatal(err)
	}

	return status, reason
}

func (h *harness) eventCount(kind string) int {
	h.t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx, "select count(*) from events where kind = $1", kind).Scan(&n); err != nil {
		h.t.Fatal(err)
	}

	return n
}

// A cloud that stops accepting the connected key (deleted, expired,
// rotated by hand) shows as rejected on the Cloud page once the inventory
// scan finds out, and as connected again once the cloud takes it back.
func TestRejectedCredentialIsMarkedInvalid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	if launched := h.launch(); len(launched) != 1 {
		t.Fatalf("launched = %d", len(launched))
	}

	connected, _ := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: "AKIATEST", SecretAccessKey: "secret"})
	h.aws.BadSecret = string(connected)
	h.aws.FailList = true
	if err := h.svc.ReconcileOnce(h.ctx); err == nil {
		t.Fatal("reconcile hid the failed list")
	}
	status, reason := h.credentialStatus()
	if status != "invalid" || reason == "" {
		t.Fatalf("status = %q, %q; want invalid with the cloud's reason", status, reason)
	}
	if n := h.eventCount("cloud.credentials_rejected"); n != 1 {
		t.Fatalf("rejected events = %d", n)
	}

	// A later failing scan within the check interval neither checks again
	// nor records the rejection twice.
	if err := h.svc.ReconcileOnce(h.ctx); err == nil {
		t.Fatal("reconcile hid the failed list")
	}
	if n := h.eventCount("cloud.credentials_rejected"); n != 1 {
		t.Fatalf("rejected events after a second scan = %d", n)
	}

	h.aws.BadSecret = ""
	h.aws.FailList = false
	h.reconcile()
	if status, reason := h.credentialStatus(); status != "ok" || reason != "" {
		t.Fatalf("status = %q, %q; want ok again", status, reason)
	}
	if n := h.eventCount("cloud.credentials_accepted"); n != 1 {
		t.Fatalf("accepted events = %d", n)
	}
}

// A list that fails for any other reason (the cloud unreachable,
// throttling) says nothing about the key: it stays connected.
func TestFailedListWithWorkingCredentialStaysConnected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	if launched := h.launch(); len(launched) != 1 {
		t.Fatalf("launched = %d", len(launched))
	}

	h.aws.FailList = true
	if err := h.svc.ReconcileOnce(h.ctx); err == nil {
		t.Fatal("reconcile hid the failed list")
	}
	if status, _ := h.credentialStatus(); status != "ok" {
		t.Fatalf("status = %q, want ok", status)
	}
	if n := h.eventCount("cloud.credentials_rejected"); n != 0 {
		t.Fatalf("rejected events = %d", n)
	}
}

// Reconnecting replaces a rejected credential and clears the rejection.
func TestReconnectClearsRejection(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.queue()
	if launched := h.launch(); len(launched) != 1 {
		t.Fatalf("launched = %d", len(launched))
	}
	connected, _ := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: "AKIATEST", SecretAccessKey: "secret"})
	h.aws.BadSecret = string(connected)
	h.aws.FailList = true
	_ = h.svc.ReconcileOnce(h.ctx)
	if status, _ := h.credentialStatus(); status != "invalid" {
		t.Fatalf("status = %q, want invalid", status)
	}

	h.aws.FailList = false
	rotated, _ := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: "AKIAROTATED", SecretAccessKey: "secret2"})
	if _, err := h.svc.ConnectCloud(h.ctx, installationID, cloud.AWS, rotated, region, nil); err != nil {
		t.Fatal(err)
	}
	if status, reason := h.credentialStatus(); status != "ok" || reason != "" {
		t.Fatalf("status = %q, %q; want ok after reconnecting", status, reason)
	}
}

// With no machine running there is nothing for the scan to list: a launch
// that the cloud refuses is what finds out the key no longer works.
func TestLaunchRefusalMarksCredentialInvalid(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	connected, _ := awscloud.EncodeSecret(awscloud.Secret{AccessKeyID: "AKIATEST", SecretAccessKey: "secret"})
	h.aws.BadSecret = string(connected)
	h.aws.FailLaunch = func(cloud.Launch) error {
		return errors.New("AuthFailure: AWS was not able to validate the provided access credentials")
	}
	h.queue()
	if launched := h.launch(); len(launched) != 0 {
		t.Fatalf("launched = %d", len(launched))
	}
	if status, reason := h.credentialStatus(); status != "invalid" || reason == "" {
		t.Fatalf("status = %q, %q; want invalid", status, reason)
	}
	if n := h.eventCount("cloud.credentials_rejected"); n != 1 {
		t.Fatalf("rejected events = %d", n)
	}
}
