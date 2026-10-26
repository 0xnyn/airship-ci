package fleet

import (
	"encoding/json"
	"fmt"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
	"github.com/wizenheimer/airship/apps/api/internal/db"

	"github.com/google/uuid"
)

// Definition sources, mirroring runners.source.
const (
	SourceProfile      = "profile"
	SourceRepository   = "repository"
	SourceOrganization = "organization"
)

// Spec is the machine a runner launches as. It is fixed when the runner is
// created, so editing a profile or config never changes a runner in flight.
type Spec struct {
	Arch           string            `json:"arch"`
	InstanceType   string            `json:"instanceType"`
	Image          string            `json:"image,omitempty"`
	DiskGB         int               `json:"diskGb"`
	DiskType       string            `json:"diskType,omitempty"`
	DiskIOPS       int               `json:"diskIops,omitempty"`
	DiskThroughput int               `json:"diskThroughput,omitempty"`
	Spot           bool              `json:"spot"`
	Env            map[string]string `json:"env,omitempty"`
	Privileged     bool              `json:"privileged,omitempty"`
}

// Attempt is one way to serve a job: a definition, a machine and the data
// plane to launch it on.
type Attempt struct {
	Source string `json:"source"`
	// Name is the profile label or config runner name.
	Name        string     `json:"name"`
	ProfileID   *uuid.UUID `json:"profileId,omitempty"`
	DataPlaneID uuid.UUID  `json:"dataPlaneId"`
	Spec        Spec       `json:"spec"`
	Labels      []string   `json:"labels"`
}

// Plan is a job's attempts in fallback order. A failed launch moves on to
// the next attempt; past the end it starts over, up to the attempt cap.
type Plan []Attempt

// maxAttempts is how many failed launches a plan gets: every attempt once,
// and at least the configured minimum.
func (p Plan) maxAttempts(minimum int64) int64 {
	return max(minimum, int64(len(p)))
}

// at returns the attempt to use after failed failures.
func (p Plan) at(failed int64) Attempt {
	return p[int(failed%int64(len(p)))]
}

func encodePlan(p Plan) ([]byte, error) {
	if len(p) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encode plan: %w", err)
	}

	return raw, nil
}

func decodePlan(raw []byte) (Plan, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode plan: %w", err)
	}

	return p, nil
}

func decodeSpec(raw []byte) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(raw, &s); err != nil {
		return Spec{}, fmt.Errorf("decode spec: %w", err)
	}

	return s, nil
}

// profileAttempt is the single attempt for a dashboard profile.
func profileAttempt(profile db.Profile) Attempt {
	return Attempt{
		Source:      SourceProfile,
		Name:        profile.Label,
		ProfileID:   uuidPtr(profile.ID),
		DataPlaneID: profile.DataPlaneID,
		Spec: Spec{
			Arch:         profile.Arch,
			InstanceType: profile.InstanceType,
			Image:        deref(profile.Image),
			DiskGB:       int(profile.DiskGb),
			Spot:         profile.Spot,
		},
		Labels: RunnerLabels(profile),
	}
}

// launchProfile is what the agent is told to launch.
func (s Spec) launchProfile(label string) agentproto.LaunchProfile {
	return agentproto.LaunchProfile{
		Label:          label,
		Arch:           s.Arch,
		InstanceType:   s.InstanceType,
		Image:          s.Image,
		DiskGB:         s.DiskGB,
		DiskType:       s.DiskType,
		DiskIOPS:       s.DiskIOPS,
		DiskThroughput: s.DiskThroughput,
		Spot:           s.Spot,
		Env:            s.Env,
		Privileged:     s.Privileged,
	}
}
