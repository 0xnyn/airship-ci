"use client";

import type { Schemas } from "@airship/client";
import { Badge } from "@airship/ui/components/badge";
import { Button, buttonVariants } from "@airship/ui/components/button";
import { cn } from "@airship/ui/lib/utils";
import { ArrowRight, ExternalLink } from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useState } from "react";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { CloudTile, ConnectCloud, cloudName, cloudOrder } from "./cloud";
import { OnboardingLayout, StepPanel, StepSkeleton } from "./onboarding";
import { useOrg } from "./org-context";
import { Panel, PanelList } from "./panel";
import { RepositoryPicker } from "./repository-picker";
import { CloudStatus } from "./status";

const SETUP_STEPS = ["github", "cloud", "activate", "workflows"] as const;
type SetupStep = (typeof SETUP_STEPS)[number];

const titles: Record<SetupStep, { hint: string; title: string }> = {
  activate: {
    hint: "Let a repository launch runners",
    title: "Activate a repository",
  },
  cloud: { hint: "Where runners launch", title: "Connect a cloud" },
  github: {
    hint: "Which repositories Airship sees",
    title: "Install the GitHub App",
  },
  workflows: { hint: "Point jobs at your runners", title: "Move workflows" },
};

function isStep(value: string | null): value is SetupStep {
  return SETUP_STEPS.includes(value as SetupStep);
}

/** Getting an organization from nothing to its first job, one step at a time. */
export function Setup() {
  const { org } = useOrg();
  const router = useRouter();
  const params = useSearchParams();
  const path = { params: { path: { org: org.login } } };
  const repos = $api.useQuery("get", "/v1/orgs/{org}/repositories", path);
  const clouds = $api.useQuery("get", "/v1/orgs/{org}/clouds", path);

  const repoItems = repos.data?.items ?? [];
  const done: Record<SetupStep, boolean> = {
    activate: repoItems.some((repo) => repo.activated),
    cloud: (clouds.data?.items ?? []).some((cloud) => cloud.status === "ok"),
    github: repoItems.some((repo) => repo.accessible),
    workflows: false,
  };
  const loaded = repos.isSuccess && clouds.isSuccess;
  const asked = params.get("step");
  const firstOpen =
    SETUP_STEPS.find((candidate) => !done[candidate]) ?? "workflows";
  const step: SetupStep = isStep(asked) ? asked : firstOpen;
  const go = (target: SetupStep) =>
    router.replace(`/${org.login}/setup?step=${target}` as Route, {
      scroll: false,
    });
  const advance = (from: SetupStep) => {
    const later = SETUP_STEPS.slice(SETUP_STEPS.indexOf(from) + 1);
    go(later.find((candidate) => !done[candidate]) ?? "workflows");
  };

  const finished = SETUP_STEPS.filter((s) => done[s]).length;

  return (
    <OnboardingLayout
      aside={
        loaded
          ? `${finished} of ${SETUP_STEPS.length - 1} done. Skip anything and come back.`
          : null
      }
      back={{ href: `/${org.login}`, label: "Dashboard" }}
      current={step}
      description="From install to your first job on your own runners"
      onVisit={go}
      steps={SETUP_STEPS.map((id) => ({
        done: done[id],
        hint: titles[id].hint,
        id,
        title: titles[id].title,
      }))}
      title="Set up Airship"
    >
      {loaded ? null : <StepSkeleton />}
      {loaded && step === "github" ? (
        <GitHubStep onNext={() => advance("github")} repositories={repoItems} />
      ) : null}
      {loaded && step === "cloud" ? (
        <CloudStep
          clouds={clouds.data?.items ?? []}
          onBack={() => go("github")}
          onNext={() => advance("cloud")}
        />
      ) : null}
      {loaded && step === "activate" ? (
        <ActivateStep
          onBack={() => go("cloud")}
          onNext={() => advance("activate")}
          repositories={repoItems}
        />
      ) : null}
      {loaded && step === "workflows" ? (
        <WorkflowsStep onBack={() => go("activate")} repositories={repoItems} />
      ) : null}
      {repos.isError || clouds.isError ? (
        <p className="text-danger-foreground text-sm" role="alert">
          {errorMessage(
            repos.error ?? clouds.error,
            "Could not load your setup"
          )}
        </p>
      ) : null}
    </OnboardingLayout>
  );
}

function Footer({
  next,
  nextLabel = "Continue",
  onBack,
  onNext,
}: {
  next?: boolean;
  nextLabel?: string;
  onBack?: () => void;
  onNext: () => void;
}) {
  return (
    <>
      {onBack ? (
        <Button onClick={onBack} variant="ghost">
          Back
        </Button>
      ) : (
        <span />
      )}
      <div className="flex gap-2">
        {next ? null : (
          <Button onClick={onNext} variant="ghost">
            Skip
          </Button>
        )}
        <Button onClick={onNext} variant={next ? "default" : "outline"}>
          {nextLabel}
          <ArrowRight aria-hidden="true" />
        </Button>
      </div>
    </>
  );
}

function GitHubStep({
  onNext,
  repositories,
}: {
  onNext: () => void;
  repositories: Schemas["Repository"][];
}) {
  const { org } = useOrg();
  const config = $api.useQuery("get", "/v1/auth/config");
  const visible = repositories.filter((repo) => repo.accessible).length;
  const installUrl = config.data?.installUrl;

  return (
    <StepPanel
      description="Airship only sees the repositories you grant it"
      footer={<Footer next={visible > 0} onNext={onNext} />}
      title={titles.github.title}
    >
      <Panel className="flex flex-wrap items-center gap-4 p-5">
        <span className="grid min-w-0 flex-1 gap-1">
          <span className="font-medium text-sm">
            Installed on <span className="font-mono">{org.login}</span>
          </span>
          <span className="text-muted-foreground text-sm">
            {visible === 1
              ? "1 repository visible"
              : `${visible} repositories visible`}
          </span>
        </span>
        {installUrl ? (
          <a
            className={buttonVariants({ variant: "outline" })}
            href={installUrl}
            rel="noopener noreferrer"
            target="_blank"
          >
            Choose repositories
            <ExternalLink aria-hidden="true" />
          </a>
        ) : null}
      </Panel>
    </StepPanel>
  );
}

function CloudStep({
  clouds,
  onBack,
  onNext,
}: {
  clouds: Schemas["Cloud"][];
  onBack: () => void;
  onNext: () => void;
}) {
  const first = clouds.find((cloud) => cloud.status === "ok")?.cloud;
  const [picked, setPicked] = useState(first ?? cloudOrder[0] ?? "aws");
  const byName = new Map(clouds.map((cloud) => [cloud.cloud, cloud]));

  return (
    <StepPanel
      description="Runners launch in an account you own. Credentials are encrypted and never shown again."
      footer={
        <Footer next={first !== undefined} onBack={onBack} onNext={onNext} />
      }
      title={titles.cloud.title}
    >
      <fieldset
        aria-label="Providers"
        className="m-0 grid min-w-0 grid-cols-2 gap-2 border-0 p-0 sm:grid-cols-3 xl:grid-cols-4"
      >
        {cloudOrder.map((name) => {
          const existing = byName.get(name);
          return (
            <button
              aria-pressed={picked === name}
              className={cn(
                "flex items-center gap-3 rounded-lg border bg-card p-3 text-left transition-colors hover:bg-muted",
                picked === name && "border-foreground ring-1 ring-foreground"
              )}
              key={name}
              onClick={() => setPicked(name)}
              type="button"
            >
              <CloudTile name={name} size="sm" />
              <span className="grid min-w-0 gap-0.5">
                <span className="truncate font-medium text-sm">
                  {cloudName(name)}
                </span>
                <span className="truncate text-xs">
                  {existing ? (
                    <CloudStatus cloud={existing} />
                  ) : (
                    <span className="text-muted-foreground">Not connected</span>
                  )}
                </span>
              </span>
            </button>
          );
        })}
      </fieldset>
      <ConnectCloud key={picked} name={picked} />
    </StepPanel>
  );
}

function ActivateStep({
  onBack,
  onNext,
  repositories,
}: {
  onBack: () => void;
  onNext: () => void;
  repositories: Schemas["Repository"][];
}) {
  const { isAdmin, org } = useOrg();
  const router = useRouter();
  const any = repositories.some((repo) => repo.activated);
  const activateHref = (repo: Schemas["Repository"]) =>
    `/${org.login}/repositories/new?repo=${repo.id}&step=options&from=setup` as Route;

  return (
    <StepPanel
      description={
        isAdmin
          ? "Only activated repositories launch machines in your cloud"
          : "Only organization owners can activate repositories"
      }
      footer={<Footer next={any} onBack={onBack} onNext={onNext} />}
      title={titles.activate.title}
    >
      <RepositoryPicker
        action={(repo) => {
          if (repo.activated) {
            return <Badge variant="success">Activated</Badge>;
          }
          if (!isAdmin) {
            return null;
          }
          return (
            <Button
              nativeButton={false}
              render={<Link href={activateHref(repo)} />}
              size="sm"
              variant="outline"
            >
              Activate
            </Button>
          );
        }}
        onPick={isAdmin ? (repo) => router.push(activateHref(repo)) : undefined}
        pickable={(repo) => !repo.activated}
      />
    </StepPanel>
  );
}

function WorkflowsStep({
  onBack,
  repositories,
}: {
  onBack: () => void;
  repositories: Schemas["Repository"][];
}) {
  const { isAdmin, org } = useOrg();
  const activated = repositories.filter((repo) => repo.activated);
  const migrate = (repo?: number) =>
    (repo
      ? `/${org.login}/repositories/migrate?repo=${repo}`
      : `/${org.login}/repositories/migrate`) as Route;

  return (
    <StepPanel
      description="Airship reads your workflows, suggests runners, and opens a pull request. Nothing changes until you merge."
      footer={
        <>
          <Button onClick={onBack} variant="ghost">
            Back
          </Button>
          <Link
            className={buttonVariants({ variant: "outline" })}
            href={`/${org.login}` as Route}
          >
            Finish
          </Link>
        </>
      }
      title={titles.workflows.title}
    >
      {isAdmin ? null : (
        <Panel className="p-6 text-muted-foreground text-sm">
          Only organization owners can move workflows
        </Panel>
      )}
      {isAdmin && activated.length > 0 ? (
        <PanelList>
          {activated.map((repo) => (
            <li className="flex items-center gap-3 px-4 py-3" key={repo.id}>
              <span className="min-w-0 flex-1 truncate font-medium text-sm">
                {repo.fullName}
              </span>
              <Link
                className={buttonVariants({ size: "sm" })}
                href={migrate(repo.id)}
              >
                Move workflows
              </Link>
            </li>
          ))}
        </PanelList>
      ) : null}
      {isAdmin ? (
        <Link
          className="w-fit text-muted-foreground text-sm underline-offset-4 hover:text-foreground hover:underline"
          href={migrate()}
        >
          {activated.length > 0
            ? "Or pick another repository →"
            : "Pick a repository →"}
        </Link>
      ) : null}
    </StepPanel>
  );
}
