package gcp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// TestAgainstLiveGCP launches one small instance in a real project and
// deletes it: the check gcptest cannot stand in for, since the fake was
// written from the API reference rather than observed behaviour. It runs
// when AIRSHIP_LIVE_GCP_KEY_FILE names a service account key (Compute
// Admin, Compute Engine API enabled) and AIRSHIP_LIVE_GCP_LOCATION a zone
// or region; it costs a machine's minutes.
func TestAgainstLiveGCP(t *testing.T) {
	keyFile, location := os.Getenv("AIRSHIP_LIVE_GCP_KEY_FILE"), os.Getenv("AIRSHIP_LIVE_GCP_LOCATION")
	if keyFile == "" || location == "" {
		t.Skip("AIRSHIP_LIVE_GCP_* is not set")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := EncodeSecret(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := ParseSecret(secret)
	region, zone, ok := SplitLocation(location)
	if !ok {
		t.Fatalf("%q is not a zone or region", location)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	p := New(Options{})

	identity, err := p.Verify(ctx, secret, location)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("project %s as %s", identity.AccountID, identity.Principal)

	launch := testLaunch()
	launch.Region = EncodeScope(region, parsed.ProjectID, parsed.ProjectID)
	launch.InstallationID = time.Now().Unix()
	launch.RunnerName = "airship-live-" + strings.ToLower(time.Now().Format("0102150405"))
	launch.Machine = cloud.Machine{InstanceType: "e2-micro", Arch: "x64", Disk: cloud.Disk{SizeGB: 10}, Zone: zone}
	id, err := p.Launch(ctx, secret, launch)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Logf("launched %s", id)
	defer func() {
		if err := p.Terminate(context.WithoutCancel(ctx), secret, launch.Region, id, launch.InstallationID); err != nil {
			t.Errorf("terminate %s: %v (delete it by hand)", id, err)
		}
	}()

	// The same runner again is the same machine, not a second one.
	again, err := p.Launch(ctx, secret, launch)
	if err != nil || again != id {
		t.Fatalf("repeated launch = %s, %v; want %s", again, err, id)
	}

	listed, err := p.List(ctx, secret, launch.Region, launch.InstallationID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, instance := range listed {
		found = found || (instance.InstanceID == id && instance.RunnerID == launch.RunnerID)
	}
	if !found {
		t.Fatalf("launched instance not listed under its installation label: %+v", listed)
	}

	// Another installation may not delete it.
	if err := p.Terminate(ctx, secret, launch.Region, id, launch.InstallationID+1); err == nil {
		t.Fatal("an instance was deleted by an installation that does not own it")
	}
	if err := p.Terminate(ctx, secret, launch.Region, id, launch.InstallationID); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if err := p.Terminate(ctx, secret, launch.Region, id, launch.InstallationID); err != nil {
		t.Fatalf("terminate again: %v", err)
	}
}
