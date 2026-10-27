// Package bootscript renders the script a runner machine runs at startup:
// install the GitHub Actions runner, register it with its single-use JIT
// config, run one job, shut down.
//
// Everything interpolated into a script is validated first. The script
// runs as root (or SYSTEM), so a value that could break out of its quoting
// would be remote code execution on the customer's machine.
package bootscript

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	jitPattern     = regexp.MustCompile(`^[A-Za-z0-9+/=]+$`)
	// Windows paths: a drive, then letters, digits, dots, dashes,
	// underscores and backslashes. No spaces or quotes.
	windowsPathPattern = regexp.MustCompile(`^[A-Za-z]:\\[A-Za-z0-9_.\\-]{1,200}$`)
	windowsUserPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,20}$`)
	passwordPattern    = regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`)
	urlPattern         = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._/-]*)?$`)
	tokenPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
)

// DefaultWindowsPath is where the runner is installed on Windows.
const DefaultWindowsPath = `C:\actions-runner`

// DefaultWindowsUser is the account interactive runners log in as.
const DefaultWindowsUser = "runner"

// Variables the runner itself reads; letting a config set them would let a
// file change how the runner talks to GitHub.
var reservedEnvPrefixes = []string{"GITHUB_", "RUNNER_", "ACTIONS_"}

// CheckEnv rejects job environment variables that could reconfigure the
// runner or smuggle line breaks into the files it reads.
func CheckEnv(env map[string]string) error {
	for name, value := range env {
		if !validEnvName(name) {
			return fmt.Errorf("invalid environment variable name %q", name)
		}
		upper := strings.ToUpper(name)
		for _, prefix := range reservedEnvPrefixes {
			if strings.HasPrefix(upper, prefix) {
				return fmt.Errorf("environment variable %s is reserved for the runner", name)
			}
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("environment variable %s contains a line break", name)
		}
	}

	return nil
}

func validEnvName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for i, c := range name {
		letter := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}

	return true
}

// envFile renders the runner's .env, base64 encoded so nothing in it is
// ever shell or PowerShell.
func envFile(env map[string]string) (string, error) {
	if err := CheckEnv(env); err != nil {
		return "", err
	}
	if len(env) == 0 {
		return "", nil
	}
	var out strings.Builder
	for _, name := range slices.Sorted(maps.Keys(env)) {
		out.WriteString(name + "=" + env[name] + "\n")
	}

	return base64.StdEncoding.EncodeToString([]byte(out.String())), nil
}

func check(boot cloud.Boot, arch string) error {
	if !versionPattern.MatchString(boot.RunnerVersion) {
		return fmt.Errorf("invalid runner version %q", boot.RunnerVersion)
	}
	if arch != "x64" && arch != "arm64" {
		return fmt.Errorf("invalid arch %q", arch)
	}
	if !jitPattern.MatchString(boot.JITConfig) {
		return errors.New("jit config is not base64")
	}

	return nil
}

// The Linux boot script. The instance is launched so that shutting down
// terminates it, and every exit path below ends with a shutdown: the job
// finishing, the runner crashing, the script failing, or the hard deadline.
var linuxTemplate = template.Must(template.New("linux").Parse(`#!/bin/bash
set -euo pipefail
trap 'shutdown -P now' EXIT

# Hard deadline: power off even if the control plane is unreachable.
shutdown -P +{{.DeadlineMinutes}} || true

RUNNER_VERSION='{{.RunnerVersion}}'
RUNNER_ARCH='{{.Arch}}'
RUNNER_DIR=/opt/actions-runner

# Package installs must never prompt, and must wait for the lock that
# unattended-upgrades holds on a fresh image rather than fail on it.
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
mkdir -p /etc/apt/apt.conf.d
echo 'DPkg::Lock::Timeout "600";' > /etc/apt/apt.conf.d/90airship-lock

id -u runner >/dev/null 2>&1 || useradd --create-home --shell /bin/bash runner
echo 'runner ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/runner
chmod 0440 /etc/sudoers.d/runner
getent group docker >/dev/null && usermod -aG docker runner
{{- if .GPU}}

# The runner definition asked for a GPU: make sure the NVIDIA driver is
# there. Images that ship one (NVIDIA's AMIs) skip this.
if ! command -v nvidia-smi >/dev/null 2>&1; then
  apt-get update -qq && apt-get install -y -qq ubuntu-drivers-common
  ubuntu-drivers install --gpgpu || ubuntu-drivers install || true
  modprobe nvidia || true
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
{{- if .CacheURL}}

# Cache acceleration: a local proxy serves GitHub's cache API from an S3
# bucket in this account. It is trusted only on this machine; if any step
# fails the job still runs, just without the accelerator.
if (
  set -e
  mkdir -p /etc/airship
  curl --fail --silent --show-error --location --retry 5 \
    -o /usr/local/bin/airship-cacheproxy "{{.CacheURL}}/v1/assets/cacheproxy/linux/${RUNNER_ARCH}"
  chmod 0755 /usr/local/bin/airship-cacheproxy
  AIRSHIP_URL='{{.CacheURL}}' AIRSHIP_RUNNER_TOKEN='{{.CacheToken}}' \
    setsid nohup /usr/local/bin/airship-cacheproxy -ca-out /etc/airship/cacheproxy-ca.pem \
    > /var/log/airship-cacheproxy.log 2>&1 &
  for _ in $(seq 1 50); do
    [ -s /etc/airship/cacheproxy-ca.pem ] && curl -fsS http://127.0.0.1:6420/healthz > /dev/null && break
    sleep 0.2
  done
  [ -s /etc/airship/cacheproxy-ca.pem ]
  curl -fsS http://127.0.0.1:6420/healthz > /dev/null
  cp /etc/airship/cacheproxy-ca.pem /usr/local/share/ca-certificates/airship-cacheproxy.crt
  update-ca-certificates > /dev/null
  {
    echo 'HTTPS_PROXY=http://127.0.0.1:6421'
    echo 'https_proxy=http://127.0.0.1:6421'
    echo 'NO_PROXY=127.0.0.1,localhost,169.254.169.254'
    echo 'no_proxy=127.0.0.1,localhost,169.254.169.254'
    echo 'NODE_EXTRA_CA_CERTS=/etc/airship/cacheproxy-ca.pem'
    echo 'ACTIONS_CACHE_URL=http://127.0.0.1:6420/'
  } >> "$RUNNER_DIR/.env"
); then
  echo "cache proxy ready"
else
  echo "cache proxy unavailable; continuing without it" >&2
fi
{{- end}}
chown -R runner:runner "$RUNNER_DIR"

# The runner reads ACTIONS_RUNNER_INPUT_JITCONFIG as its --jitconfig
# argument and removes it from the environment before any job step runs.
cd "$RUNNER_DIR"
sudo --user=runner --set-home \
  env ACTIONS_RUNNER_INPUT_JITCONFIG='{{.JITConfig}}' \
  ./run.sh || true
`))

type linuxParams struct {
	RunnerVersion   string
	Arch            string
	JITConfig       string
	DeadlineMinutes int
	EnvFile         string
	GPU             bool
	CacheURL        string
	CacheToken      string
}

// Linux renders the boot script for a Linux image.
func Linux(boot cloud.Boot, arch string) (string, error) {
	if err := check(boot, arch); err != nil {
		return "", err
	}
	env, err := envFile(boot.Env)
	if err != nil {
		return "", err
	}
	params := linuxParams{
		RunnerVersion:   boot.RunnerVersion,
		Arch:            arch,
		JITConfig:       boot.JITConfig,
		DeadlineMinutes: max(boot.MaxLifetimeSeconds/60, 1),
		EnvFile:         env,
		GPU:             boot.GPU,
	}
	if boot.Cache != nil {
		if !urlPattern.MatchString(boot.Cache.ControlPlaneURL) {
			return "", fmt.Errorf("invalid control plane url %q", boot.Cache.ControlPlaneURL)
		}
		if !tokenPattern.MatchString(boot.Cache.Token) {
			return "", errors.New("invalid cache token")
		}
		params.CacheURL = strings.TrimRight(boot.Cache.ControlPlaneURL, "/")
		params.CacheToken = boot.Cache.Token
	}
	var out bytes.Buffer
	if err := linuxTemplate.Execute(&out, params); err != nil {
		return "", fmt.Errorf("render boot script: %w", err)
	}

	return out.String(), nil
}

// The Windows boot script, run by EC2Launch as SYSTEM. The runner is
// downloaded, given its .env, and either run in place or, for interactive
// runners, registered as a logon task for a local account the machine then
// logs in as.
var windowsTemplate = template.Must(template.New("windows").Parse(`<powershell>
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$PSNativeCommandUseErrorActionPreference = $false
# Any failure below powers the machine off (and so terminates it).
trap { shutdown.exe /s /t 0 /c "Airship runner setup failed"; break }
# Hard deadline: shut down even if the control plane is unreachable.
shutdown.exe /s /t {{.DeadlineSeconds}} /c "Airship runner deadline"

$dir = "{{.Path}}"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$zip = Join-Path $env:TEMP "actions-runner.zip"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -Uri "https://github.com/actions/runner/releases/download/v{{.RunnerVersion}}/actions-runner-win-{{.Arch}}-{{.RunnerVersion}}.zip" -OutFile $zip
Expand-Archive -Path $zip -DestinationPath $dir -Force
{{- if .EnvFile}}
# Job environment variables from the runner's config, base64 so nothing in
# them is PowerShell.
[IO.File]::WriteAllBytes((Join-Path $dir ".env"), [Convert]::FromBase64String("{{.EnvFile}}"))
{{- end}}
$jit = "{{.JITConfig}}"
{{- if .Interactive}}

# Interactive: a local account logs in automatically and a logon task
# starts the runner inside that desktop session.
$user = "{{.User}}"
$password = "{{.Password}}"
$secure = ConvertTo-SecureString $password -AsPlainText -Force
if (-not (Get-LocalUser -Name $user -ErrorAction SilentlyContinue)) {
  New-LocalUser -Name $user -Password $secure -PasswordNeverExpires -AccountNeverExpires | Out-Null
}
Add-LocalGroupMember -Group "Administrators" -Member $user -ErrorAction SilentlyContinue
icacls $dir /grant "${user}:(OI)(CI)F" | Out-Null
$winlogon = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon"
Set-ItemProperty $winlogon -Name AutoAdminLogon -Value "1" -Type String
Set-ItemProperty $winlogon -Name DefaultUserName -Value $user -Type String
Set-ItemProperty $winlogon -Name DefaultPassword -Value $password -Type String
Set-ItemProperty $winlogon -Name AutoLogonCount -Value 1 -Type DWord
# The scheduled deadline does not survive the reboot, and a pending one
# would refuse it: cancel it here and arm it again at logon, before the
# runner starts, so the task's time limit is not the only bound.
$action = New-ScheduledTaskAction -Execute "cmd.exe" -Argument "/c shutdown.exe /s /t {{.DeadlineSeconds}} /c AirshipRunnerDeadline & $dir\run.cmd --jitconfig $jit & shutdown.exe /s /t 0" -WorkingDirectory $dir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Seconds {{.DeadlineSeconds}})
Register-ScheduledTask -TaskName "AirshipRunner" -Action $action -Trigger $trigger -Settings $settings -User $user -Password $password -RunLevel Highest -Force | Out-Null
shutdown.exe /a
Restart-Computer -Force
{{- else}}

try {
  & "$dir\run.cmd" --jitconfig $jit
} finally {
  shutdown.exe /s /t 0
}
{{- end}}
</powershell>
`))

type windowsParams struct {
	RunnerVersion   string
	Arch            string
	JITConfig       string
	DeadlineSeconds int
	EnvFile         string
	Path            string
	Interactive     bool
	User            string
	Password        string
}

// Windows renders the boot script for a Windows image. password is the
// local account's password for interactive runners; it must be
// alphanumeric and is ignored otherwise.
func Windows(boot cloud.Boot, arch, password string) (string, error) {
	if err := check(boot, arch); err != nil {
		return "", err
	}
	env, err := envFile(boot.Env)
	if err != nil {
		return "", err
	}
	params := windowsParams{
		RunnerVersion:   boot.RunnerVersion,
		Arch:            arch,
		JITConfig:       boot.JITConfig,
		DeadlineSeconds: max(boot.MaxLifetimeSeconds, 60),
		EnvFile:         env,
		Path:            boot.Windows.Path,
		Interactive:     boot.Windows.Interactive,
		User:            boot.Windows.User,
		Password:        password,
	}
	if params.Path == "" {
		params.Path = DefaultWindowsPath
	}
	if !windowsPathPattern.MatchString(params.Path) {
		return "", fmt.Errorf("invalid runner path %q", params.Path)
	}
	if params.Interactive {
		if params.User == "" {
			params.User = DefaultWindowsUser
		}
		if !windowsUserPattern.MatchString(params.User) {
			return "", fmt.Errorf("invalid runner user %q", params.User)
		}
		if !passwordPattern.MatchString(params.Password) {
			return "", errors.New("interactive runners need an alphanumeric password")
		}
	}
	var out bytes.Buffer
	if err := windowsTemplate.Execute(&out, params); err != nil {
		return "", fmt.Errorf("render boot script: %w", err)
	}

	return out.String(), nil
}
