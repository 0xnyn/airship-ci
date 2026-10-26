package aws

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"text/template"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

// EC2 rejects user data over 16 KiB (before base64).
const maxUserDataBytes = 16 << 10

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	jitPattern     = regexp.MustCompile(`^[A-Za-z0-9+/=]+$`)
)

// The boot script. Everything interpolated into it is validated first: it
// runs as root, so a value that could break out of its quoting would be
// remote code execution on the customer's instance.
//
// The instance is launched with InstanceInitiatedShutdownBehavior=terminate,
// so every exit path below ends with the machine deleted: the job finishing,
// the runner crashing, the script failing, or the hard deadline.
var userDataTemplate = template.Must(template.New("userdata").Parse(`#!/bin/bash
set -euo pipefail
trap 'shutdown -P now' EXIT

# Hard deadline: power off even if the control plane is unreachable.
shutdown -P +{{.DeadlineMinutes}} || true

RUNNER_VERSION='{{.RunnerVersion}}'
RUNNER_ARCH='{{.Arch}}'
RUNNER_DIR=/opt/actions-runner

id -u runner >/dev/null 2>&1 || useradd --create-home --shell /bin/bash runner
echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner
chmod 0440 /etc/sudoers.d/runner
getent group docker >/dev/null && usermod -aG docker runner

# Prefer a runner baked into the image; download the requested release
# otherwise. GitHub refuses runners more than 30 days behind a release.
if [ "$(cat "$RUNNER_DIR/.version" 2>/dev/null || true)" != "$RUNNER_VERSION" ]; then
  rm -rf "$RUNNER_DIR"
  mkdir -p "$RUNNER_DIR"
  curl --fail --silent --show-error --location --retry 5 \
    "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${RUNNER_ARCH}-${RUNNER_VERSION}.tar.gz" \
    | tar -xz -C "$RUNNER_DIR"
  "$RUNNER_DIR/bin/installdependencies.sh"
  echo "$RUNNER_VERSION" > "$RUNNER_DIR/.version"
fi
chown -R runner:runner "$RUNNER_DIR"

# The runner reads ACTIONS_RUNNER_INPUT_JITCONFIG as its --jitconfig
# argument and removes it from the environment before any job step runs.
cd "$RUNNER_DIR"
sudo --user=runner --set-home \
  env ACTIONS_RUNNER_INPUT_JITCONFIG='{{.JITConfig}}' \
  ./run.sh || true
`))

type userDataParams struct {
	RunnerVersion   string
	Arch            string
	JITConfig       string
	DeadlineMinutes int
}

// userData renders the boot script for a launch, base64 encoded for EC2.
func userData(launch agentproto.Launch) (string, error) {
	params := userDataParams{
		RunnerVersion:   launch.RunnerVersion,
		Arch:            launch.Profile.Arch,
		JITConfig:       launch.JITConfig,
		DeadlineMinutes: max(launch.MaxLifetimeSeconds/60, 1),
	}
	if !versionPattern.MatchString(params.RunnerVersion) {
		return "", fmt.Errorf("invalid runner version %q", params.RunnerVersion)
	}
	if params.Arch != "x64" && params.Arch != "arm64" {
		return "", fmt.Errorf("invalid arch %q", params.Arch)
	}
	if !jitPattern.MatchString(params.JITConfig) {
		return "", errors.New("jit config is not base64")
	}

	var out bytes.Buffer
	if err := userDataTemplate.Execute(&out, params); err != nil {
		return "", fmt.Errorf("render user data: %w", err)
	}
	if out.Len() > maxUserDataBytes {
		return "", fmt.Errorf("user data is %d bytes, over EC2's %d byte limit", out.Len(), maxUserDataBytes)
	}

	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
