package daytona

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	toolbox "github.com/daytona/clients/toolbox-api-client-go"
)

// TestAgainstLiveDaytona launches one small sandbox in a real Daytona
// organization and watches it end: the check daytonatest cannot stand in
// for. The boot script it runs cannot register (the JIT config is not
// GitHub's), so the runner exits and the script stops the sandbox, which
// Daytona deletes; that is the lifecycle's shutdown path, verified for
// real. It runs when AIRSHIP_LIVE_DAYTONA_API_KEY is set (and optionally
// AIRSHIP_LIVE_DAYTONA_API_URL, AIRSHIP_LIVE_DAYTONA_REGION) and costs a
// few sandbox minutes.
func TestAgainstLiveDaytona(t *testing.T) {
	key := os.Getenv("AIRSHIP_LIVE_DAYTONA_API_KEY")
	if key == "" {
		t.Skip("AIRSHIP_LIVE_DAYTONA_API_KEY is not set")
	}
	region := os.Getenv("AIRSHIP_LIVE_DAYTONA_REGION")
	if region == "" {
		region = DefaultRegion
	}
	secret, err := EncodeSecret(Secret{APIKey: key, APIURL: os.Getenv("AIRSHIP_LIVE_DAYTONA_API_URL")})
	if err != nil {
		t.Fatal(err)
	}
	// TestMain shortens the polls for the fake; the real service needs the
	// production bounds.
	startTimeout, pollInterval = 4*time.Minute, 2*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	p := New(Options{})

	identity, err := p.Verify(ctx, secret, region)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("organization %s as %s", identity.AccountID, identity.Principal)
	if _, err := p.Verify(ctx, secret, "no-such-region"); err == nil {
		t.Error("an unknown region was accepted")
	}

	launch := testLaunch()
	launch.Region = region
	launch.InstallationID = time.Now().Unix()
	launch.RunnerName = "airship-live-" + strings.ToLower(time.Now().Format("0102150405"))
	launch.Boot.MaxLifetimeSeconds = 900
	launch.Machine.Disk.SizeGB = DefaultDiskGB // the smallest tier's per-sandbox maximum
	started := time.Now()
	id, err := p.Launch(ctx, secret, launch)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Logf("launched %s in %s", id, time.Since(started).Round(time.Second))
	defer func() {
		if err := p.Terminate(context.WithoutCancel(ctx), secret, region, id, launch.InstallationID); err != nil {
			t.Errorf("terminate %s: %v (delete it by hand)", id, err)
		}
	}()
	c, _ := p.clientsFor(secret)

	// The same runner again is the same sandbox, not a second one.
	again, err := p.Launch(ctx, secret, launch)
	if err != nil || again != id {
		t.Fatalf("repeated launch = %s, %v; want %s", again, err, id)
	}

	// Daytona's list is eventually consistent (a new sandbox shows up
	// within seconds; its documentation allows for more). The control
	// plane's instance grace covers this; here the list is retried.
	found := false
	listStarted := time.Now()
	for !found && time.Since(listStarted) < 90*time.Second {
		listed, err := p.List(ctx, secret, region, launch.InstallationID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, instance := range listed {
			found = found || (instance.InstanceID == id && instance.RunnerID == launch.RunnerID && !instance.Stopped)
		}
		if !found {
			time.Sleep(3 * time.Second)
		}
	}
	if !found {
		t.Fatalf("launched sandbox not listed under its installation label within %s", time.Since(listStarted).Round(time.Second))
	}
	t.Logf("listed after %s", time.Since(listStarted).Round(time.Second))

	// Another installation may not delete it.
	if err := p.Terminate(ctx, secret, region, id, launch.InstallationID+1); err == nil {
		t.Fatal("a sandbox was deleted by an installation that does not own it")
	}

	// The runner cannot register, so the script ends and stops the
	// sandbox, and Daytona deletes it. Watch that happen, with the boot
	// log for the record.
	var lastLog string
	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		sb, err := c.get(ctx, id)
		if isNotFound(err) {
			// The last log read is from before the stop (the sandbox is
			// gone with its files); the disappearance is the proof.
			t.Logf("sandbox gone after %s; last boot log read:\n%s", time.Since(started).Round(time.Second), lastLog)
			if !strings.Contains(lastLog, "Setting up") && !strings.Contains(lastLog, "actions-runner") && !strings.Contains(lastLog, "Listening") {
				t.Errorf("the boot log shows no sign of the script having run")
			}

			return
		}
		if err != nil {
			t.Logf("get: %v", err)
		} else {
			state := ""
			if sb.State != nil {
				state = string(*sb.State)
			}
			if state == "started" {
				if out := readLog(ctx, c, sb.ToolboxProxyUrl, sb.Id, launch.RunnerID); out != "" {
					lastLog = out
				}
			}
			t.Logf("%s: %s", time.Since(started).Round(time.Second), state)
		}
		time.Sleep(5 * time.Second)
	}
	t.Errorf("the sandbox did not stop itself within the deadline; last boot log:\n%s", lastLog)
}

func readLog(ctx context.Context, c *clients, proxyURL, sandboxID, runnerID string) string {
	tb, err := c.toolbox(proxyURL, sandboxID)
	if err != nil {
		return ""
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	out, _, err := tb.ProcessAPI.ExecuteCommand(ctx).Request(toolbox.ExecuteRequest{
		Command: "cat /tmp/airship-boot-" + runnerID + ".log 2>/dev/null | tail -c 6000", Timeout: ptr(int32(20)),
	}).Execute()
	if err != nil || out == nil {
		return ""
	}

	return out.Result
}
