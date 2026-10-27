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

// Repository as it appears in webhook payloads and the REST API.
type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
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

// InstallationRepositoriesEvent is the installation_repositories webhook
// payload: repositories added to or removed from the app's access.
type InstallationRepositoriesEvent struct {
	Action              string          `json:"action"`
	Installation        InstallationRef `json:"installation"`
	RepositoriesAdded   []Repository    `json:"repositories_added"`
	RepositoriesRemoved []Repository    `json:"repositories_removed"`
}

// PushEvent is the push webhook payload, trimmed to what Airship uses.
type PushEvent struct {
	Ref          string           `json:"ref"`
	Repository   Repository       `json:"repository"`
	Installation *InstallationRef `json:"installation"`
}

// RepositoryEvent is the repository webhook payload: renamed, made public
// or private, archived, deleted and the like.
type RepositoryEvent struct {
	Action       string           `json:"action"`
	Repository   Repository       `json:"repository"`
	Installation *InstallationRef `json:"installation"`
}
