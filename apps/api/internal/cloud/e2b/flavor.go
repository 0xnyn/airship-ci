package e2b

import (
	"regexp"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// Flavor is one cloud that speaks E2B's protocol: its control plane at
// api.<domain>, each sandbox's envd at 49983-<id>.<domain>, the same
// sandbox, list and process APIs. Vendors differ in their domain, key
// format, which create path they serve and how long a sandbox may live.
type Flavor struct {
	// Cloud is the name in config files and the credential's cloud.
	Cloud string
	// Title is the vendor's name in messages.
	Title string
	// DefaultDomain is the hosted service.
	DefaultDomain string
	// KeyPattern is what an API key looks like.
	KeyPattern *regexp.Regexp
	// KeyHint describes the key format in messages.
	KeyHint string
	// CreatePath is the create endpoint: E2B's /v2/sandboxes, which takes an
	// Idempotency-Key, or /sandboxes on clouds that serve the first API.
	CreatePath string
	// Idempotent says the create endpoint honours Idempotency-Key.
	Idempotent bool
	// LongRunning sets Novita's long_running metadata, without which the
	// timeout is capped at the plan's session length (1-4 hours).
	LongRunning bool
	// Secure asks for an envd access token on the first API (E2B's v2 is
	// always secured).
	Secure bool
	// MaxTimeout is the longest timeout the cloud accepts.
	MaxTimeout time.Duration
}

// The E2B-protocol clouds Airship supports.
var (
	// FlavorE2B is E2B itself.
	FlavorE2B = Flavor{
		Cloud:         cloud.E2B,
		Title:         "E2B",
		DefaultDomain: "e2b.app",
		KeyPattern:    regexp.MustCompile(`^e2b_[A-Za-z0-9_-]{8,512}$`),
		KeyHint:       "an API key (e2b_...)",
		CreatePath:    "/v2/sandboxes",
		Idempotent:    true,
		MaxTimeout:    24 * time.Hour,
	}
	// FlavorNovita is Novita's sandbox service, a fork of E2B's. Its SDK
	// creates through the first API, and a sandbox past the plan's session
	// length needs long_running metadata.
	FlavorNovita = Flavor{
		Cloud:         cloud.Novita,
		Title:         "Novita",
		DefaultDomain: "us-phx-1.sandbox.novita.ai",
		KeyPattern:    regexp.MustCompile(`^[A-Za-z0-9_.-]{16,512}$`),
		KeyHint:       "a Novita API key",
		CreatePath:    "/sandboxes",
		LongRunning:   true,
		Secure:        true,
		MaxTimeout:    24 * time.Hour,
	}
	// FlavorLelantos is Lelantos, E2B-compatible Firecracker sandboxes in
	// the EU. Keys are lel_<hex>, or the same key as e2b_<hex>.
	FlavorLelantos = Flavor{
		Cloud:         cloud.Lelantos,
		Title:         "Lelantos",
		DefaultDomain: "lelantos.ai",
		KeyPattern:    regexp.MustCompile(`^(lel|e2b)_[A-Za-z0-9_-]{8,512}$`),
		KeyHint:       "an API key (lel_... or e2b_...)",
		CreatePath:    "/sandboxes",
		Secure:        true,
		MaxTimeout:    24 * time.Hour,
	}
)

// Flavors lists every E2B-protocol cloud.
var Flavors = []Flavor{FlavorE2B, FlavorNovita, FlavorLelantos}

// FlavorFor returns the flavour of a cloud name.
func FlavorFor(cloudName string) (Flavor, bool) {
	for _, f := range Flavors {
		if f.Cloud == cloudName {
			return f, true
		}
	}

	return Flavor{}, false
}
