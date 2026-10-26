package fleet

import (
	"encoding/json"
	"fmt"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/runnerconfig"

	"github.com/google/uuid"
)

// Definition sources, mirroring runners.source.
const (
	SourceRepository   = "repository"
	SourceOrganization = "organization"
)

// Spec is the machine a runner launches as. It is fixed when the runner is
// created, so editing a config never changes a runner in flight.
type Spec struct {
	Arch         string            `json:"arch"`
	InstanceType string            `json:"instanceType"`
	Image        string            `json:"image,omitempty"`
	Spot         bool              `json:"spot"`
	Disk         cloud.Disk        `json:"disk"`
	Env          map[string]string `json:"env,omitempty"`
	GPU          bool              `json:"gpu,omitempty"`
	Cache        bool              `json:"cache,omitempty"`
	ProvisionVia string            `json:"provisionVia,omitempty"`
	Windows      cloud.Windows     `json:"windows,omitempty"`
	SubnetID     string            `json:"subnetId,omitempty"`
	// SecurityGroupIDs and InstanceProfile override the cloud's defaults.
	SecurityGroupIDs []string `json:"securityGroupIds,omitempty"`
	InstanceProfile  string   `json:"instanceProfile,omitempty"`
}

func specFor(r runnerconfig.Runner, v runnerconfig.Variant) Spec {
	return Spec{
		Arch:         v.Arch,
		InstanceType: v.InstanceType,
		Image:        v.Image,
		Spot:         v.Spot,
		Disk:         cloud.Disk{SizeGB: v.Disk.SizeGB, Type: v.Disk.Type, IOPS: v.Disk.IOPS, Throughput: v.Disk.Throughput, InitRate: v.Disk.InitRate},
		Env:          r.Env,
		GPU:          r.GPU,
		Cache:        r.Cache,
		ProvisionVia: r.ProvisionVia,
		Windows:      cloud.Windows{Interactive: r.Windows.Interactive, User: r.Windows.User, Path: r.Windows.Path},
		SubnetID:     r.SubnetID,
		// Copied so a later change to the parsed file cannot alias a stored spec.
		SecurityGroupIDs: append([]string(nil), r.SecurityGroupIDs...),
		InstanceProfile:  r.InstanceProfile,
	}
}

// machine is the provider's view of a spec.
func (s Spec) machine() cloud.Machine {
	return cloud.Machine{
		InstanceType: s.InstanceType, Image: s.Image, Arch: s.Arch, Spot: s.Spot, Disk: s.Disk,
		SubnetID: s.SubnetID, SecurityGroupIDs: s.SecurityGroupIDs, InstanceProfile: s.InstanceProfile,
	}
}

// Attempt is one way to serve a job: a definition, a machine, and the
// cloud credential and region to launch it with.
type Attempt struct {
	Source string `json:"source"`
	// Name is the config runner name.
	Name         string    `json:"name"`
	Cloud        string    `json:"cloud"`
	CredentialID uuid.UUID `json:"credentialId"`
	Region       string    `json:"region"`
	Spec         Spec      `json:"spec"`
	Labels       []string  `json:"labels"`
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
