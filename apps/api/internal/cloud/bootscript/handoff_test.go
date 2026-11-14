package bootscript

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func cleanup(t *testing.T, runnerID string) {
	t.Helper()
	t.Cleanup(func() {
		for _, suffix := range []string{".sh", ".ready", ".log", ".running.sh", ".sh.part"} {
			_ = os.Remove(strings.TrimSuffix(ScriptPath(runnerID), ".sh") + suffix)
		}
	})
}

// The waiter runs in a real shell: it waits for the written script and
// becomes it, so the script's exit status is the entrypoint's, and the
// script was moved aside.
func TestWaiterAndWriteCommand(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	cleanup(t, id)
	waiter := exec.CommandContext(t.Context(), "/bin/sh", "-c", Waiter(id))
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	write := exec.CommandContext(t.Context(), "/bin/sh", "-c", WriteCommand(id))
	write.Env = append(os.Environ(), BootEnv+"="+EncodeScript("echo booted-$((1+1)); exit 3\n"))
	if out, err := write.CombinedOutput(); err != nil {
		t.Fatalf("write: %v: %s", err, out)
	}
	err := waiter.Wait()
	var exitErr *exec.ExitError
	if err == nil || !asExit(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("waiter exit = %v, want 3", err)
	}
	if log, _ := os.ReadFile(LogPath(id)); !strings.Contains(string(log), "booted-2") {
		t.Errorf("log = %q", log)
	}
	if _, err := os.Stat(ScriptPath(id)); err == nil {
		t.Error("the script was not moved aside")
	}
	if info, err := os.Stat(strings.TrimSuffix(ScriptPath(id), ".sh") + ".running.sh"); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("running script = %v, %v", info, err)
	}
}

// The detached start returns at once and the script runs on.
func TestStartDetachedCommand(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	cleanup(t, id)
	start := exec.CommandContext(t.Context(), "/bin/sh", "-c", StartDetachedCommand(id))
	start.Env = append(os.Environ(), BootEnv+"="+EncodeScript("sleep 0.3; echo detached-ok\n"))
	began := time.Now()
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start: %v: %s", err, out)
	}
	if time.Since(began) > 250*time.Millisecond {
		t.Error("the start waited for the script")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if log, _ := os.ReadFile(LogPath(id)); strings.Contains(string(log), "detached-ok") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the detached script never finished")
}

func asExit(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError) //nolint:errorlint // exec returns it unwrapped
	if ok {
		*target = e
	}

	return ok
}
