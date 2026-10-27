package bootscript

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// pwshPath finds PowerShell: AIRSHIP_PWSH, or pwsh on PATH.
func pwshPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("AIRSHIP_PWSH"); p != "" {
		return p
	}
	p, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed; set AIRSHIP_PWSH to a PowerShell binary")
	}

	return p
}

// parseScript runs PowerShell's own parser over a rendered script and
// returns its parse errors.
func parseScript(t *testing.T, pwsh, script string) string {
	t.Helper()
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(script), "<powershell>"), "</powershell>")
	const parse = `$errors = $null
$script = [Console]::In.ReadToEnd()
[System.Management.Automation.Language.Parser]::ParseInput($script, [ref]$null, [ref]$errors) | Out-Null
foreach ($e in $errors) { Write-Output ("{0}: {1}" -f $e.Extent.StartLineNumber, $e.Message) }`
	cmd := exec.CommandContext(context.Background(), pwsh, "-NoProfile", "-NonInteractive", "-Command", parse)
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pwsh: %v: %s", err, out)
	}

	return strings.TrimSpace(string(out))
}

// Both Windows scripts (background and run_as: interactive) must parse
// under PowerShell, including the trap and the re-armed deadline.
func TestWindowsScriptsParse(t *testing.T) {
	t.Parallel()
	pwsh := pwshPath(t)
	boot := testBoot()
	boot.Env = map[string]string{"A": "b"}
	for name, windows := range map[string]cloud.Windows{
		"background":  {},
		"interactive": {Interactive: true, User: "runner", Path: `C:\actions-runner`},
	} {
		b := boot
		b.Windows = windows
		script, err := Windows(b, "x64", "Abcdefghij1234567890")
		if err != nil {
			t.Fatal(err)
		}
		if problems := parseScript(t, pwsh, script); problems != "" {
			t.Errorf("%s script does not parse:\n%s\n---\n%s", name, problems, script)
		}
		if name == "interactive" && (!strings.Contains(script, "shutdown.exe /a") || !strings.Contains(script, "/c shutdown.exe /s /t 3600")) {
			t.Errorf("interactive script does not cancel and re-arm the deadline:\n%s", script)
		}
		if !strings.Contains(script, "trap {") {
			t.Errorf("%s script has no trap", name)
		}
	}
}
