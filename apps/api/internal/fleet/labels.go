package fleet

import (
	"strings"

	"github.com/wizenheimer/airship/apps/api/internal/db"
)

// Skip reasons recorded on jobs that asked for an Airship label but will not
// get a runner. They match the jobs.skip_reason check constraint.
const (
	SkipPublicRepository      = "public_repository"
	SkipUnsatisfiableLabels   = "unsatisfiable_labels"
	SkipAmbiguousLabels       = "ambiguous_labels"
	SkipInstallationSuspended = "installation_suspended"
)

// RunnerLabels are the labels a runner for profile registers with. GitHub
// only hands a job to a runner whose labels include every runs-on label.
func RunnerLabels(profile db.Profile) []string {
	return []string{"self-hosted", "linux", profile.Arch, profile.Label}
}

// Match is the outcome of matching a job's runs-on labels to profiles.
type Match struct {
	Profile    *db.Profile
	SkipReason string
}

// MatchProfile decides which profile, if any, serves a job.
//
// candidates are the installation's profiles whose label appears in the
// job's labels. A job is served only when exactly one profile matches and
// the runner it would launch carries every label the job asks for; anything
// else would start a machine GitHub never hands the job to.
func MatchProfile(jobLabels []string, candidates []db.Profile, privateRepo bool) Match {
	if len(candidates) == 0 {
		return Match{}
	}
	if len(candidates) > 1 {
		return Match{SkipReason: SkipAmbiguousLabels}
	}
	profile := candidates[0]

	have := map[string]bool{}
	for _, label := range RunnerLabels(profile) {
		have[strings.ToLower(label)] = true
	}
	// GitHub also accepts these spellings of the default labels.
	if profile.Arch == "x64" {
		have["x86_64"] = true
	}
	for _, label := range jobLabels {
		if !have[strings.ToLower(label)] {
			return Match{SkipReason: SkipUnsatisfiableLabels}
		}
	}

	if !privateRepo && !profile.AllowPublicRepos {
		return Match{SkipReason: SkipPublicRepository}
	}

	return Match{Profile: &profile}
}

// lowerAll returns labels lowercased; profile labels are stored lowercase
// and GitHub compares labels case-insensitively.
func lowerAll(labels []string) []string {
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		out = append(out, strings.ToLower(label))
	}

	return out
}
