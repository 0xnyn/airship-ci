package gcp

import (
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"
)

// The config validator (SDK-free, so it cannot share this package's code)
// and the provider must agree on what a location is: a region the
// validator accepts and the provider rejects would store a runner that can
// never launch, as happened with two-digit regions (europe-west10).
func TestLocationsAgreeWithTheConfigValidator(t *testing.T) {
	t.Parallel()
	for _, location := range []string{
		"us-central1", "us-central1-b", "europe-west10", "europe-west12-a", "northamerica-northeast1-a",
		"me-central2", "africa-south1-a", "us-east5-b",
		"us-east-1", "us-central1-bb", "europe", "us-central1-", "US-CENTRAL1",
	} {
		_, _, providerOK := SplitLocation(location)
		_, err := runnerconfig.Parse("airship.yaml", []byte(`
runners:
  - name: r
    cloud: gcp
    instance_type: n1-standard-4
    region: "`+location+`"
    labels: [airship-r]
`))
		if validatorOK := err == nil; validatorOK != providerOK {
			t.Errorf("%q: validator accepts = %v, provider accepts = %v (%v)", location, validatorOK, providerOK, err)
		}
	}
}
