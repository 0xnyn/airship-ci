package azure

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// TestAgainstLiveAzure launches one small virtual machine in a real
// subscription and deletes it: the check azuretest cannot stand in for,
// since the fake was written from the API reference rather than observed
// behaviour. It runs when AIRSHIP_LIVE_AZURE_SUBSCRIPTION_ID,
// AIRSHIP_LIVE_AZURE_TENANT_ID, AIRSHIP_LIVE_AZURE_CLIENT_ID and
// AIRSHIP_LIVE_AZURE_CLIENT_SECRET name a service principal with the
// Contributor role (and optionally AIRSHIP_LIVE_AZURE_REGION a region,
// eastus by default); it costs a machine's minutes and leaves the
// airship-runners resource group and its per-region network behind, as a
// real deployment would.
func TestAgainstLiveAzure(t *testing.T) {
	secret, err := EncodeSecret(Secret{
		SubscriptionID: os.Getenv("AIRSHIP_LIVE_AZURE_SUBSCRIPTION_ID"),
		TenantID:       os.Getenv("AIRSHIP_LIVE_AZURE_TENANT_ID"),
		ClientID:       os.Getenv("AIRSHIP_LIVE_AZURE_CLIENT_ID"),
		ClientSecret:   os.Getenv("AIRSHIP_LIVE_AZURE_CLIENT_SECRET"),
	})
	if err != nil {
		t.Skip("AIRSHIP_LIVE_AZURE_* is not set: " + err.Error())
	}
	region := os.Getenv("AIRSHIP_LIVE_AZURE_REGION")
	if region == "" {
		region = DefaultRegion
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	p := New(Options{})

	identity, err := p.Verify(ctx, secret, region)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("subscription %s as application %s", identity.AccountID, identity.Principal)
	if _, err := p.Verify(ctx, secret, "nowhere"); err == nil {
		t.Fatal("an unknown region was accepted")
	}

	launch := testLaunch()
	launch.Region = region
	launch.InstallationID = time.Now().Unix()
	launch.RunnerID = strings.ToLower(time.Now().Format("20060102-1504-0500-8000-000000000000"))[:36]
	launch.RunnerName = "airship-live-" + strings.ToLower(time.Now().Format("0102150405"))
	launch.Machine = cloud.Machine{InstanceType: "Standard_B2s", Arch: "x64"}
	started := time.Now()
	id, err := p.Launch(ctx, secret, launch)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Logf("launched %s in %s", id, time.Since(started).Round(time.Second))
	defer func() {
		if err := p.Terminate(context.WithoutCancel(ctx), secret, region, id, launch.InstallationID); err != nil {
			t.Errorf("terminate %s: %v (delete it by hand, with its -nic, -ip and -osdisk)", id, err)
		}
	}()

	// The same runner again is the same machine, not a second one.
	again, err := p.Launch(ctx, secret, launch)
	if err != nil || again != id {
		t.Fatalf("repeated launch = %s, %v; want %s", again, err, id)
	}

	// The list carries the machine with its power state.
	found := false
	for attempt := 0; attempt < 12 && !found; attempt++ {
		listed, err := p.List(ctx, secret, region, launch.InstallationID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, instance := range listed {
			if instance.InstanceID == id && instance.RunnerID == launch.RunnerID {
				found = true
				if instance.Stopped || instance.Stopping {
					t.Fatalf("a machine just launched lists as stopped or stopping: %+v", instance)
				}
			}
		}
		if !found {
			time.Sleep(5 * time.Second)
		}
	}
	if !found {
		t.Fatal("launched machine not listed under its installation tag")
	}

	// Another installation may not delete it.
	if err := p.Terminate(ctx, secret, region, id, launch.InstallationID+1); err == nil {
		t.Fatal("a machine was deleted by an installation that does not own it")
	}
	if err := p.Terminate(ctx, secret, region, id, launch.InstallationID); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if err := p.Terminate(ctx, secret, region, id, launch.InstallationID); err != nil {
		t.Fatalf("terminate again: %v", err)
	}
	// The deletion takes its interface and address with it; the sweep then
	// has nothing to do.
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		listed, err := p.List(ctx, secret, region, launch.InstallationID)
		if err != nil {
			t.Fatalf("list after terminate: %v", err)
		}
		if len(listed) == 0 {
			break
		}
		time.Sleep(10 * time.Second)
	}
	if err := p.Sweep(ctx, secret, region, launch.InstallationID); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}
