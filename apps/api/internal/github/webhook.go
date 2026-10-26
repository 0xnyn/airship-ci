package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifySignature checks an X-Hub-Signature-256 header against the raw
// request body in constant time.
func VerifySignature(secret []byte, body []byte, header string) bool {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)

	return hmac.Equal(got, mac.Sum(nil))
}

// Sign returns the X-Hub-Signature-256 value for body. Tests and fakes use
// it to send webhooks GitHub would send.
func Sign(secret []byte, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Repository as it appears in webhook payloads.
type Repository struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// InstallationRef is the installation a webhook was sent for.
type InstallationRef struct {
	ID int64 `json:"id"`
}

// WorkflowJobEvent is the workflow_job webhook payload.
type WorkflowJobEvent struct {
	Action       string           `json:"action"`
	WorkflowJob  WorkflowJob      `json:"workflow_job"`
	Repository   Repository       `json:"repository"`
	Installation *InstallationRef `json:"installation"`
}

// InstallationEvent is the installation webhook payload.
type InstallationEvent struct {
	Action       string       `json:"action"`
	Installation Installation `json:"installation"`
}
