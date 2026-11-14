"use client";

import type { Schemas } from "@airship/client";
import { Button, buttonVariants } from "@airship/ui/components/button";
import { cn } from "@airship/ui/lib/utils";
import { ArrowRight, CircleCheck, TriangleAlert } from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";

import { $api } from "@/lib/api";

import {
  type CloudName,
  CloudTile,
  ConnectCloud,
  cloudGroups,
  cloudName,
  cloudOrder,
} from "./cloud";
import { OnboardingLayout, StepPanel, StepSkeleton } from "./onboarding";
import { useOrg } from "./org-context";
import { Panel } from "./panel";
import { CloudStatus } from "./status";

const CLOUDS = "/v1/orgs/{org}/clouds";

const STEPS = ["provider", "credentials", "done"] as const;
type Step = (typeof STEPS)[number];

const copy: Record<Step, { hint: string; title: string }> = {
  credentials: { hint: "Verified before saving", title: "Credentials" },
  done: { hint: "What to do next", title: "Finish" },
  provider: { hint: "Your account or a sandbox cloud", title: "Provider" },
};

function isStep(value: string | null): value is Step {
  return STEPS.includes(value as Step);
}

function isCloud(value: string | null): value is CloudName {
  return cloudOrder.includes(value as CloudName);
}

/** Connecting a cloud: pick a provider, paste a credential, see it verified. */
export function ConnectCloudFlow() {
  const { isAdmin, org } = useOrg();
  const router = useRouter();
  const params = useSearchParams();
  const clouds = $api.useQuery("get", CLOUDS, {
    params: { path: { org: org.login } },
  });

  const askedCloud = params.get("cloud");
  const picked = isCloud(askedCloud) ? askedCloud : null;
  const existing = clouds.data?.items?.find((item) => item.cloud === picked);
  const asked = params.get("step");
  let step: Step = isStep(asked) ? asked : "provider";
  if (!picked) {
    step = "provider";
  } else if (step === "done" && !existing) {
    step = "credentials";
  }

  const go = (next: Step, name: CloudName | null = picked) => {
    const query = new URLSearchParams();
    if (name) {
      query.set("cloud", name);
    }
    query.set("step", next);
    router.replace(`/${org.login}/cloud/connect?${query}` as Route, {
      scroll: false,
    });
  };

  const steps = STEPS.map((id) => ({
    disabled:
      !isAdmin ||
      (id !== "provider" && !picked) ||
      (id === "done" && !existing),
    done:
      (id === "provider" && picked !== null) ||
      (id === "credentials" && existing !== undefined),
    hint: id === "provider" && picked ? cloudName(picked) : copy[id].hint,
    id,
    title: copy[id].title,
  }));

  return (
    <OnboardingLayout
      back={{ href: `/${org.login}/cloud`, label: "Cloud" }}
      current={step}
      description="Runners launch in an account you own"
      onVisit={(next) => go(next)}
      steps={steps}
      title="Connect cloud"
    >
      {isAdmin ? null : (
        <Panel className="p-6 text-muted-foreground text-sm">
          Only organization owners can connect a cloud
        </Panel>
      )}
      {isAdmin && clouds.isPending ? <StepSkeleton /> : null}
      {isAdmin && clouds.isSuccess && step === "provider" ? (
        <ProviderStep
          clouds={clouds.data.items ?? []}
          onPick={(name) => go("credentials", name)}
          picked={picked}
        />
      ) : null}
      {isAdmin && clouds.isSuccess && picked && step === "credentials" ? (
        <CredentialsStep
          existing={existing}
          key={picked}
          name={picked}
          onBack={() => go("provider")}
          onNext={() => go("done")}
        />
      ) : null}
      {isAdmin && picked && existing && step === "done" ? (
        <DoneStep cloud={existing} />
      ) : null}
    </OnboardingLayout>
  );
}

function ProviderStep({
  clouds,
  onPick,
  picked,
}: {
  clouds: Schemas["Cloud"][];
  onPick: (name: CloudName) => void;
  picked: CloudName | null;
}) {
  const byName = new Map(clouds.map((cloud) => [cloud.cloud, cloud]));
  return (
    <StepPanel
      description="Your own AWS, Google Cloud or Azure account, or a sandbox cloud"
      title="Choose a provider"
    >
      {cloudGroups.map((group) => (
        <fieldset className="m-0 grid gap-2 border-0 p-0" key={group.label}>
          <legend className="mb-2 font-medium text-muted-foreground text-xs">
            {group.label}
          </legend>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-3">
            {group.names.map((name) => {
              const existing = byName.get(name);
              return (
                <button
                  aria-pressed={picked === name}
                  className={cn(
                    "group flex items-center gap-3 rounded-lg border bg-card p-3 text-left outline-none transition-colors hover:border-foreground/30 hover:bg-muted/40 focus-visible:ring-1 focus-visible:ring-ring",
                    picked === name &&
                      "border-foreground ring-1 ring-foreground"
                  )}
                  key={name}
                  onClick={() => onPick(name)}
                  type="button"
                >
                  <CloudTile name={name} size="sm" />
                  <span className="grid min-w-0 flex-1 gap-0.5">
                    <span className="truncate font-medium text-sm">
                      {cloudName(name)}
                    </span>
                    <span className="truncate text-xs">
                      {existing ? (
                        <CloudStatus cloud={existing} />
                      ) : (
                        <span className="text-muted-foreground">
                          Not connected
                        </span>
                      )}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        </fieldset>
      ))}
    </StepPanel>
  );
}

function CredentialsStep({
  existing,
  name,
  onBack,
  onNext,
}: {
  existing: Schemas["Cloud"] | undefined;
  name: CloudName;
  onBack: () => void;
  onNext: () => void;
}) {
  // Moves on by itself once a credential is saved, not for one that was
  // already there when the step opened.
  const [baseline] = useState(existing?.updatedAt ?? null);
  const updatedAt = existing?.updatedAt ?? null;
  useEffect(() => {
    if (updatedAt !== null && updatedAt !== baseline) {
      onNext();
    }
  }, [baseline, onNext, updatedAt]);

  return (
    <StepPanel
      description="Airship checks the credential before saving it and encrypts it at rest"
      footer={
        <>
          <Button onClick={onBack} variant="ghost">
            Back
          </Button>
          {existing ? (
            <Button onClick={onNext}>
              Continue
              <ArrowRight aria-hidden="true" />
            </Button>
          ) : null}
        </>
      }
      title={`Connect ${cloudName(name)}`}
    >
      <ConnectCloud name={name} />
    </StepPanel>
  );
}

function DoneStep({ cloud }: { cloud: Schemas["Cloud"] }) {
  const { org } = useOrg();
  const healthy = cloud.status === "ok";
  return (
    <StepPanel
      description="Repositories you activate can launch runners here now"
      footer={
        <>
          <Link
            className={buttonVariants({ variant: "ghost" })}
            href={`/${org.login}/cloud` as Route}
          >
            Done
          </Link>
          <Link
            className={buttonVariants()}
            href={`/${org.login}/repositories/new` as Route}
          >
            Add a repository
            <ArrowRight aria-hidden="true" />
          </Link>
        </>
      }
      title={
        healthy
          ? `${cloudName(cloud.cloud)} is connected`
          : "Connected, with a problem"
      }
    >
      <Panel className="flex items-start gap-3 p-5">
        {healthy ? (
          <CircleCheck
            aria-hidden="true"
            className="mt-0.5 size-5 shrink-0 text-success"
          />
        ) : (
          <TriangleAlert
            aria-hidden="true"
            className="mt-0.5 size-5 shrink-0 text-warning"
          />
        )}
        <div className="grid min-w-0 gap-1 text-sm">
          <p className="font-medium">
            <span className="font-mono">{cloud.identity}</span>
            {cloud.accountId ? (
              <span className="text-muted-foreground">
                {" "}
                in <span className="font-mono">{cloud.accountId}</span>
              </span>
            ) : null}
          </p>
          <p className="text-muted-foreground">
            {cloud.error ?? `Default region ${cloud.defaultRegion}`}
          </p>
        </div>
      </Panel>
    </StepPanel>
  );
}
