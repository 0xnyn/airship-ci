package fleet_test

import (
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/db"
	"github.com/wizenheimer/airship/apps/api/internal/fleet"
)

func TestMatchProfile(t *testing.T) {
	t.Parallel()
	x64 := db.Profile{Label: "airship-4cpu", Arch: "x64"}
	public := db.Profile{Label: "airship-oss", Arch: "arm64", AllowPublicRepos: true}

	cases := []struct {
		name       string
		labels     []string
		candidates []db.Profile
		private    bool
		want       string // label served, or skip reason
	}{
		{"just the label", []string{"airship-4cpu"}, []db.Profile{x64}, true, "airship-4cpu"},
		{"with default labels", []string{"self-hosted", "Linux", "X64", "airship-4cpu"}, []db.Profile{x64}, true, "airship-4cpu"},
		{"x86_64 alias", []string{"x86_64", "airship-4cpu"}, []db.Profile{x64}, true, "airship-4cpu"},
		{"wrong arch", []string{"arm64", "airship-4cpu"}, []db.Profile{x64}, true, fleet.SkipUnsatisfiableLabels},
		{"extra label", []string{"airship-4cpu", "gpu"}, []db.Profile{x64}, true, fleet.SkipUnsatisfiableLabels},
		{"two profiles", []string{"airship-4cpu", "airship-oss"}, []db.Profile{x64, public}, true, fleet.SkipAmbiguousLabels},
		{"public repo blocked", []string{"airship-4cpu"}, []db.Profile{x64}, false, fleet.SkipPublicRepository},
		{"public repo allowed", []string{"airship-oss"}, []db.Profile{public}, false, "airship-oss"},
		{"not ours", []string{"ubuntu-latest"}, nil, true, ""},
	}
	for _, tc := range cases {
		match := fleet.MatchProfile(tc.labels, tc.candidates, tc.private)
		got := match.SkipReason
		if match.Profile != nil {
			got = match.Profile.Label
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
