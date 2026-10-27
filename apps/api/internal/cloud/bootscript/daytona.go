package bootscript

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// DockerVersion is the static Docker release installed in a Daytona sandbox
// whose definition asks for Docker and whose image has none.
const DockerVersion = "28.3.3"

// The boot script for a Daytona sandbox: a container with Daytona's daemon
// as PID 1, no init system and no power button. Stopping the daemon stops
// the sandbox, which Daytona then deletes (the sandbox is created with
// autoDeleteInterval 0), so that is this machine's shutdown, and every
// exit path ends with it. The script is written to the sandbox and started
// by the control plane through Daytona's toolbox API; it is not run at
// boot by the image, so it never runs twice on a reboot, but a repeated
// launch could start it twice, which the lock directory refuses.
var daytonaTemplate = template.Must(template.New("daytona").Parse(`#!/bin/bash
set -euo pipefail

# Daytona runs toolbox commands as the image's user: root on a plain
# container image, a sudoer on Daytona's own snapshots. Everything below
# needs root.
if [ "$(id -u)" != 0 ]; then
  exec sudo -n bash "$0" "$@"
fi

# A second start of this script (a launch repeated after a lost answer)
# must not register the runner again: its registration was single use.
# The check comes before the trap, so backing out does not stop the
# sandbox under the first runner.
if ! mkdir /var/lib/airship-boot 2>/dev/null; then
  echo "airship: the runner is already booting" >&2
  exit 0
fi

finish() {
  echo "airship: stopping the sandbox"
  kill -TERM 1 2>/dev/null || true
  sleep 60
  kill -KILL 1 2>/dev/null || true
}
trap finish EXIT

RUNNER_VERSION='{{.RunnerVersion}}'
RUNNER_ARCH='{{.Arch}}'
RUNNER_DIR=/opt/actions-runner
# Hard deadline for the job, as seconds; Daytona's own TTL on the sandbox
# is the backstop for when even this script is gone.
DEADLINE_SECONDS={{.DeadlineSeconds}}

export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a

# A plain container image has none of what the runner needs to be fetched
# and run. Install what is missing; images without apt are refused rather
# than guessed at.
missing=""
for tool in curl tar gzip sudo git; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ -s /etc/ssl/certs/ca-certificates.crt ] || missing="$missing ca-certificates"
if [ -n "$missing" ]; then
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "airship: the image lacks$missing and has no apt-get to install them; use a Debian or Ubuntu based image" >&2
    exit 1
  fi
  apt-get update -qq
  # shellcheck disable=SC2086 # a list of package names
  apt-get install -y -qq --no-install-recommends $missing
fi

id -u runner >/dev/null 2>&1 || useradd --create-home --shell /bin/bash runner
mkdir -p /etc/sudoers.d
echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner
chmod 0440 /etc/sudoers.d/runner
{{- if .GPU}}

# GPU sandboxes get the host's driver; nothing to install, only to report.
if command -v nvidia-smi >/dev/null 2>&1; then
  nvidia-smi -L || true
else
  echo "airship: no nvidia-smi in this sandbox" >&2
fi
{{- end}}
{{- if .Docker}}

# The definition asked for Docker. The sandbox is a privileged container,
# so a daemon can run inside it. If any step fails the job still runs,
# just without Docker.
if (
  set -e
  if ! command -v dockerd >/dev/null 2>&1; then
    curl --fail --silent --show-error --location --retry 5 \
      "https://download.docker.com/linux/static/stable/{{.DockerArch}}/docker-{{.DockerVersion}}.tgz" \
      | tar -xz -C /usr/local/bin --strip-components=1
  fi
  if ! command -v iptables >/dev/null 2>&1; then
    apt-get update -qq && apt-get install -y -qq --no-install-recommends iptables
  fi
  groupadd --force docker
  if ! docker info >/dev/null 2>&1; then
    setsid nohup dockerd > /var/log/airship-dockerd.log 2>&1 &
    for _ in $(seq 1 120); do
      docker info >/dev/null 2>&1 && break
      sleep 0.5
    done
  fi
  docker info >/dev/null
); then
  usermod -aG docker runner
  echo "docker ready"
else
  echo "docker unavailable; continuing without it" >&2
fi
{{- end}}

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
{{- if .EnvFile}}
# Job environment variables from the runner's config. The runner loads
# .env at startup; the content is base64 so nothing in it is shell.
echo '{{.EnvFile}}' | base64 -d >> "$RUNNER_DIR/.env"
{{- end}}
chown -R runner:runner "$RUNNER_DIR"

# The runner reads ACTIONS_RUNNER_INPUT_JITCONFIG as its --jitconfig
# argument and removes it from the environment before any job step runs.
# timeout is the deadline: past it the runner is asked to stop, then
# killed, and the trap stops the sandbox.
cd "$RUNNER_DIR"
timeout --kill-after=60 "$DEADLINE_SECONDS" \
  sudo --user=runner --set-home \
  env ACTIONS_RUNNER_INPUT_JITCONFIG='{{.JITConfig}}' \
  ./run.sh || true
`))

type daytonaParams struct {
	RunnerVersion   string
	Arch            string
	JITConfig       string
	DeadlineSeconds int
	EnvFile         string
	GPU             bool
	Docker          bool
	DockerArch      string
	DockerVersion   string
}

// Daytona renders the boot script for a Linux Daytona sandbox. docker asks
// for a Docker daemon in the sandbox. Cache acceleration is not available
// on Daytona (it has no object storage), so boot.Cache is ignored.
func Daytona(boot cloud.Boot, arch string, docker bool) (string, error) {
	if err := check(boot, arch); err != nil {
		return "", err
	}
	env, err := envFile(boot.Env)
	if err != nil {
		return "", err
	}
	params := daytonaParams{
		RunnerVersion:   boot.RunnerVersion,
		Arch:            arch,
		JITConfig:       boot.JITConfig,
		DeadlineSeconds: max(boot.MaxLifetimeSeconds, 60),
		EnvFile:         env,
		GPU:             boot.GPU,
		Docker:          docker,
		DockerArch:      "x86_64",
		DockerVersion:   DockerVersion,
	}
	if arch == "arm64" {
		params.DockerArch = "aarch64"
	}
	var out bytes.Buffer
	if err := daytonaTemplate.Execute(&out, params); err != nil {
		return "", fmt.Errorf("render boot script: %w", err)
	}

	return out.String(), nil
}
