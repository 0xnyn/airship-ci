package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
	"github.com/wizenheimer/airship/apps/api/internal/cloud/bootscript"
	"github.com/wizenheimer/airship/apps/api/internal/db"
)

// ErrNotProvisionable is returned when a runner has no boot script to hand
// out: it was not launched with provision_via: pull_from_api, has already
// fetched it, or is no longer booting.
var ErrNotProvisionable = errors.New("no boot script available for this runner")

// provisionSecret is what a pull_from_api runner needs to boot, sealed in
// the runners table until its image asks for it.
type provisionSecret struct {
	Boot cloud.Boot `json:"boot"`
	Arch string     `json:"arch"`
}

// storeProvisionSecret seals a runner's boot parameters for the developer
// API to hand out.
func (s *Service) storeProvisionSecret(ctx context.Context, runner db.Runner, boot cloud.Boot, arch string) error {
	raw, err := json.Marshal(provisionSecret{Boot: boot, Arch: arch})
	if err != nil {
		return fmt.Errorf("encode boot: %w", err)
	}
	sealed, err := s.secrets.Seal(raw)
	if err != nil {
		return fmt.Errorf("seal boot: %w", err)
	}
	if err := s.q.SetRunnerProvisionSecret(ctx, db.SetRunnerProvisionSecretParams{ID: runner.ID, ProvisionSecret: sealed}); err != nil {
		return fmt.Errorf("store boot: %w", err)
	}

	return nil
}

// ProvisionScript renders the boot script from a sealed secret handed out
// by MarkRunnerProvisioned, which is what clears it from the runner.
func (s *Service) ProvisionScript(_ context.Context, sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", ErrNotProvisionable
	}
	raw, err := s.secrets.Open(sealed)
	if err != nil {
		return "", fmt.Errorf("open boot: %w", err)
	}
	var secret provisionSecret
	if err := json.Unmarshal(raw, &secret); err != nil {
		return "", fmt.Errorf("decode boot: %w", err)
	}
	// Images that pull their script are Linux: the Windows path needs the
	// instance's own EC2Launch to run user data.
	script, err := bootscript.Linux(secret.Boot, secret.Arch)
	if err != nil {
		return "", fmt.Errorf("render boot script: %w", err)
	}

	return script, nil
}
