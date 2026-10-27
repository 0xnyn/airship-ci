package aws

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cloud"
)

// TestAgainstLiveAWS launches one small instance in a real account and
// terminates it: the only check moto cannot stand in for. It runs when
// AIRSHIP_LIVE_AWS_ACCESS_KEY_ID, AIRSHIP_LIVE_AWS_SECRET_ACCESS_KEY and
// AIRSHIP_LIVE_AWS_REGION are set, needs AmazonEC2FullAccess, a default VPC
// in the region, and costs a few cents.
func TestAgainstLiveAWS(t *testing.T) {
	keyID, secretKey, region := os.Getenv("AIRSHIP_LIVE_AWS_ACCESS_KEY_ID"), os.Getenv("AIRSHIP_LIVE_AWS_SECRET_ACCESS_KEY"), os.Getenv("AIRSHIP_LIVE_AWS_REGION")
	if keyID == "" || secretKey == "" || region == "" {
		t.Skip("AIRSHIP_LIVE_AWS_* is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := New(Options{})
	secret, _ := EncodeSecret(Secret{AccessKeyID: keyID, SecretAccessKey: secretKey})

	identity, err := p.Verify(ctx, secret, region)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("account %s as %s", identity.AccountID, identity.Principal)

	launch := testLaunch()
	launch.Region = region
	launch.InstallationID = time.Now().Unix()
	launch.Machine = cloud.Machine{InstanceType: "t3.micro", Arch: "x64", Disk: cloud.Disk{SizeGB: 8, Type: "gp3"}}
	id, err := p.Launch(ctx, secret, launch)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Logf("launched %s in %s", id, region)
	defer func() {
		if err := p.Terminate(context.WithoutCancel(ctx), secret, region, id, launch.InstallationID); err != nil {
			t.Errorf("terminate %s: %v (terminate it by hand)", id, err)
		}
	}()

	listed, err := p.List(ctx, secret, region, launch.InstallationID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, instance := range listed {
		if instance.InstanceID == id && instance.RunnerID == launch.RunnerID {
			found = true
		}
	}
	if !found {
		t.Fatalf("launched instance not listed under its installation tag: %+v", listed)
	}
}
