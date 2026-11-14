package bootscript

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// The handoff of a boot script to a sandbox whose control plane runs a
// command at start (a container entrypoint, a devbox entrypoint) but takes
// no secret there that stays private: the record of that command can be
// read back from the cloud's API. The sandbox starts a Waiter that holds
// no secret; the control plane then writes the script through the cloud's
// exec or file API and marks it ready; the waiter moves the script aside
// and becomes it, so its exit is the entrypoint's.

// BootEnv is the environment variable WriteCommand reads the script from,
// base64 encoded. An exec's environment is not echoed in its command line.
const BootEnv = "AIRSHIP_BOOT"

// waitSeconds is how long a Waiter waits for its script.
const waitSeconds = 600

// ScriptPath is where a runner's boot script is written.
func ScriptPath(runnerID string) string { return "/tmp/airship-boot-" + runnerID + ".sh" }

// ReadyPath is the marker that the script is complete.
func ReadyPath(runnerID string) string { return "/tmp/airship-boot-" + runnerID + ".ready" }

// LogPath is where the script's output goes.
func LogPath(runnerID string) string { return "/tmp/airship-boot-" + runnerID + ".log" }

// Waiter is the shell a sandbox's entrypoint runs (as /bin/sh -c): wait up
// to ten minutes for the script and its ready marker, move the script
// aside so a repeated write is never read, and exec it. With no script it
// exits 1, ending the sandbox where the cloud ends it with its entrypoint.
func Waiter(runnerID string) string {
	script, ready := ScriptPath(runnerID), ReadyPath(runnerID)
	running := strings.TrimSuffix(script, ".sh") + ".running.sh"

	return "for _ in $(seq 1 " + strconv.Itoa(waitSeconds) + "); do " +
		"if [ -e " + ready + " ] && mv " + script + " " + running + "; then exec /bin/bash " + running + " > " + LogPath(runnerID) + " 2>&1; fi; " +
		"sleep 1; done; echo \"airship: no boot script arrived\" >&2; exit 1"
}

// WriteCommand is the shell (for /bin/sh -c) that writes the script from
// BootEnv and then the ready marker. Run it with BootEnv set to
// EncodeScript(script).
func WriteCommand(runnerID string) string {
	script := ScriptPath(runnerID)

	return "umask 077 && printf '%s' \"$" + BootEnv + "\" | base64 -d > " + script + ".part && mv " + script + ".part " + script +
		" && chmod 0700 " + script + " && touch " + ReadyPath(runnerID)
}

// StartDetachedCommand is the shell that writes the script from BootEnv
// and starts it detached, for a sandbox with no entrypoint of ours. A
// repeat finds the script and does nothing: bash reads a script as it runs
// it, so a running one is never rewritten (and the script's own lock
// refuses a second start in any case).
func StartDetachedCommand(runnerID string) string {
	script := ScriptPath(runnerID)

	return "if [ -e " + script + " ]; then exit 0; fi; " +
		"umask 077 && printf '%s' \"$" + BootEnv + "\" | base64 -d > " + script + ".part && mv " + script + ".part " + script + " && chmod 0700 " + script +
		" && detach=$(command -v setsid || true)" +
		" && ($detach nohup /bin/bash " + script + " > " + LogPath(runnerID) + " 2>&1 < /dev/null &)"
}

// EncodeScript is the value of BootEnv for a script.
func EncodeScript(script string) string { return base64.StdEncoding.EncodeToString([]byte(script)) }
