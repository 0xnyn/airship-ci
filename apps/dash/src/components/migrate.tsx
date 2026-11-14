"use client";

import type { Schemas } from "@airship/client";
import { Badge } from "@airship/ui/components/badge";
import { Button, buttonVariants } from "@airship/ui/components/button";
import { Checkbox } from "@airship/ui/components/checkbox";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
import { Skeleton } from "@airship/ui/components/skeleton";
import { Switch } from "@airship/ui/components/switch";
import { cn } from "@airship/ui/lib/utils";
import {
  Check,
  Download,
  ExternalLink,
  FileCode,
  GitPullRequest,
  Lock,
  Plus,
  Search,
  Trash2,
} from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { type ReactNode, useEffect, useMemo, useState } from "react";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { diffLines, hunks } from "@/lib/line-diff";
import {
  allClouds,
  type Cloud,
  changeCloud,
  clearDraft,
  cloudTitle,
  type Draft,
  fillTargets,
  jobKey,
  jobsByKey,
  labelFor,
  listDrafts,
  loadDraft,
  machineGroup,
  nameProblem,
  newDraft,
  type RunnerDraft,
  runnerFor,
  runnerNotes,
  STEPS,
  type Step,
  saveDraft,
  servedLabels,
  shapeSuggestions,
  suggested,
  toPlan,
  unusedRunners,
  type WorkflowJob,
  type WorkflowScan,
} from "@/lib/migration";

import { ActivateDialog } from "./activate-dialog";
import { CloudLogo } from "./cloud-logo";
import { CopyButton } from "./copy-button";
import { DocsLink } from "./docs-link";
import { OnboardingLayout, StepPanel, StepSkeleton } from "./onboarding";
import { useOrg } from "./org-context";
import { Panel, PanelList } from "./panel";

const REPOSITORIES = "/v1/orgs/{org}/repositories";
const WORKFLOWS = "/v1/orgs/{org}/repositories/{repo}/workflows";

const stepTitle: Record<Step, string> = {
  jobs: "Choose jobs",
  repository: "Pick a repository",
  review: "Review and ship",
  runners: "Set up runners",
};

const stepHint: Record<Step, string> = {
  jobs: "Which jobs move to Airship",
  repository: "Any repository the app can see",
  review: "Open a pull request or copy the files",
  runners: "The machines those jobs get",
};

/** The migration assistant: a repository's workflows, moved step by step. */
export function Migrate() {
  const { isAdmin, org } = useOrg();
  const router = useRouter();
  const params = useSearchParams();
  const repoId = Number(params.get("repo")) || null;
  const repos = $api.useQuery("get", REPOSITORIES, {
    params: { path: { org: org.login } },
  });
  const repo = (repos.data?.items ?? []).find((item) => item.id === repoId);

  const choose = (id: number | null) => {
    const href = id
      ? `/${org.login}/repositories/migrate?repo=${id}`
      : `/${org.login}/repositories/migrate`;
    router.replace(href as Route);
  };

  if (!isAdmin) {
    return (
      <OnboardingLayout
        back={{ href: `/${org.login}/repositories`, label: "Repositories" }}
        current="repository"
        description={DESCRIPTION}
        onVisit={() => null}
        steps={STEPS.map((step) => ({
          disabled: true,
          hint: stepHint[step],
          id: step,
          title: stepTitle[step],
        }))}
        title="Move workflows"
      >
        <Panel className="p-6 text-muted-foreground text-sm">
          Only organization owners can move workflows
        </Panel>
      </OnboardingLayout>
    );
  }
  return (
    <Flow
      key={repoId ?? "none"}
      onChoose={choose}
      repo={repo}
      repoId={repoId}
      reposPending={repos.isPending}
    />
  );
}

const DESCRIPTION = "Move existing GitHub Actions jobs onto your own runners";

function Flow({
  onChoose,
  repo,
  repoId,
  reposPending,
}: {
  onChoose: (id: number | null) => void;
  repo: Schemas["Repository"] | undefined;
  repoId: number | null;
  reposPending: boolean;
}) {
  const { org } = useOrg();
  const scan = $api.useQuery(
    "get",
    WORKFLOWS,
    { params: { path: { org: org.login, repo: repoId ?? 0 } } },
    { enabled: repoId !== null, staleTime: 60_000 }
  );
  const [draft, setDraft] = useState<Draft | null>(null);

  // Start from the saved draft when there is one, so a person can leave
  // and come back where they were.
  useEffect(() => {
    if (repoId === null || !scan.data || draft) {
      return;
    }
    setDraft(loadDraft(org.login, repoId) ?? newDraft(scan.data));
  }, [draft, org.login, repoId, scan.data]);

  useEffect(() => {
    if (repoId !== null && draft) {
      saveDraft(org.login, repoId, draft);
    }
  }, [draft, org.login, repoId]);

  const step: Step = repoId === null ? "repository" : (draft?.step ?? "jobs");
  const go = (next: Step) => {
    if (next === "repository") {
      onChoose(null);
      return;
    }
    setDraft((current) => (current ? { ...current, step: next } : current));
  };

  const startOver = () => {
    if (repoId !== null && scan.data) {
      clearDraft(org.login, repoId);
      setDraft(newDraft(scan.data));
    }
  };

  const currentIndex = STEPS.indexOf(step);
  const steps = STEPS.map((id, index) => ({
    disabled: id !== "repository" && (repoId === null || draft === null),
    done: index < currentIndex,
    hint: id === "repository" && repo ? repo.fullName : stepHint[id],
    id,
    title: stepTitle[id],
  }));

  return (
    <OnboardingLayout
      aside={
        draft ? (
          <>
            Your choices are saved in this browser.{" "}
            <button
              className="underline-offset-4 hover:text-foreground hover:underline"
              onClick={startOver}
              type="button"
            >
              Start over
            </button>
          </>
        ) : null
      }
      back={{ href: `/${org.login}/repositories`, label: "Repositories" }}
      current={step}
      description={DESCRIPTION}
      onVisit={go}
      steps={steps}
      title="Move workflows"
    >
      {step === "repository" ? (
        <PickRepository onChoose={onChoose} pending={reposPending} />
      ) : null}
      {step !== "repository" && scan.isError ? (
        <Panel className="grid gap-3 p-6 text-sm">
          <p className="text-danger-foreground" role="alert">
            {errorMessage(scan.error, "Could not read the workflows")}
          </p>
          <div>
            <Button onClick={() => onChoose(null)} variant="outline">
              Pick another repository
            </Button>
          </div>
        </Panel>
      ) : null}
      {step !== "repository" && !scan.isError && !(scan.data && draft) ? (
        <StepSkeleton />
      ) : null}
      {scan.data && draft && repoId !== null ? (
        <StepBody
          draft={draft}
          go={go}
          repo={repo}
          repoId={repoId}
          scan={scan.data}
          setDraft={setDraft}
          step={step}
        />
      ) : null}
    </OnboardingLayout>
  );
}

function StepBody({
  draft,
  go,
  repo,
  repoId,
  scan,
  setDraft,
  step,
}: {
  draft: Draft;
  go: (step: Step) => void;
  repo: Schemas["Repository"] | undefined;
  repoId: number;
  scan: WorkflowScan;
  setDraft: (update: (draft: Draft | null) => Draft | null) => void;
  step: Step;
}) {
  const update = (change: (draft: Draft) => Draft) =>
    setDraft((current) => (current ? change(current) : current));

  if (step === "jobs") {
    return (
      <ChooseJobs
        draft={draft}
        onContinue={() => go("runners")}
        onSkip={() => go("runners")}
        scan={scan}
        update={update}
      />
    );
  }
  if (step === "runners") {
    return (
      <SetUpRunners
        draft={draft}
        onBack={() => go("jobs")}
        onContinue={() => go("review")}
        scan={scan}
        update={update}
      />
    );
  }
  return (
    <Review
      draft={draft}
      onBack={() => go("runners")}
      repo={repo}
      repoId={repoId}
      scan={scan}
    />
  );
}

// Step 1: pick a repository.

function PickRepository({
  onChoose,
  pending,
}: {
  onChoose: (id: number) => void;
  pending: boolean;
}) {
  const { org } = useOrg();
  const [query, setQuery] = useState("");
  const repos = $api.useQuery("get", REPOSITORIES, {
    params: { path: { org: org.login } },
  });
  const drafts = useMemo(() => listDrafts(org.login), [org.login]);
  const draftAt = new Map(drafts.map((draft) => [draft.repo, draft]));
  const items = (repos.data?.items ?? [])
    .filter((repo) => repo.accessible)
    .filter((repo) =>
      repo.fullName.toLowerCase().includes(query.trim().toLowerCase())
    )
    .sort(
      (a, b) =>
        (draftAt.get(b.id)?.updatedAt ?? 0) -
        (draftAt.get(a.id)?.updatedAt ?? 0)
    );

  return (
    <section className="grid gap-4">
      <div className="grid gap-1">
        <h2 className="font-semibold text-xl tracking-[-0.02em]">
          {stepTitle.repository}
        </h2>
        <p className="text-muted-foreground text-sm">
          Airship reads its workflows; nothing changes until you merge
        </p>
      </div>
      <div className="relative">
        <Search
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
        />
        <Input
          aria-label="Search repositories"
          className="pl-9"
          onChange={(event) => setQuery(event.target.value)}
          placeholder="Search…"
          value={query}
        />
      </div>
      {pending || repos.isPending ? <Skeleton className="h-48" /> : null}
      {repos.isError ? (
        <p className="text-danger-foreground text-sm" role="alert">
          {errorMessage(repos.error, "Could not list repositories")}
        </p>
      ) : null}
      {repos.data && items.length === 0 ? (
        <Panel className="p-6 text-center text-muted-foreground text-sm">
          {query
            ? "No repository matches"
            : "The app cannot see any repositories yet. Grant it access on GitHub."}
        </Panel>
      ) : null}
      {items.length > 0 ? (
        <PanelList>
          {items.map((repo) => {
            const draft = draftAt.get(repo.id);
            return (
              <li className="flex items-center gap-3 px-4 py-3" key={repo.id}>
                <span className="grid min-w-0 flex-1 gap-0.5">
                  <span className="flex min-w-0 items-center gap-2">
                    {repo.private ? (
                      <Lock
                        aria-label="Private"
                        className="size-3.5 shrink-0 text-muted-foreground"
                      />
                    ) : null}
                    <span className="truncate font-medium text-sm">
                      {repo.fullName}
                    </span>
                    {repo.activated ? (
                      <Badge variant="success">Activated</Badge>
                    ) : null}
                    {draft ? <Badge variant="info">In progress</Badge> : null}
                  </span>
                  <span className="font-mono text-muted-foreground text-xs">
                    {repo.defaultBranch}
                  </span>
                </span>
                <Button
                  onClick={() => onChoose(repo.id)}
                  size="sm"
                  variant={draft ? "default" : "outline"}
                >
                  {draft ? "Resume" : "Select"}
                </Button>
              </li>
            );
          })}
        </PanelList>
      ) : null}
    </section>
  );
}

// Step 2: choose jobs.

const kindLabel: Record<WorkflowJob["kind"], string> = {
  airship: "On Airship",
  expression: "Expression",
  group: "Runner group",
  hosted: "GitHub-hosted",
  other: "Custom label",
  reusable: "Reusable workflow",
  "self-hosted": "Self-hosted",
};

const platformLabel: Record<string, string> = {
  "linux-arm64": "Linux Arm",
  "linux-x64": "Linux",
  macos: "macOS",
  windows: "Windows",
};

function ChooseJobs({
  draft,
  onContinue,
  onSkip,
  scan,
  update,
}: {
  draft: Draft;
  onContinue: () => void;
  onSkip: () => void;
  scan: WorkflowScan;
  update: (change: (draft: Draft) => Draft) => void;
}) {
  const selected = new Set(draft.selected);
  const toggle = (keys: string[], on: boolean) =>
    update((current) => {
      const next = new Set(current.selected);
      for (const key of keys) {
        if (on) {
          next.add(key);
        } else {
          next.delete(key);
        }
      }
      return { ...current, selected: [...next] };
    });
  const movable = scan.workflows.flatMap((workflow) =>
    workflow.jobs.filter((job) => job.editable)
  ).length;

  return (
    <StepPanel
      description={
        <>
          {movable} of{" "}
          {scan.workflows.reduce((sum, w) => sum + w.jobs.length, 0)} jobs in{" "}
          <span className="font-mono text-foreground">
            {scan.defaultBranch}
          </span>{" "}
          can move automatically. GitHub-hosted Linux jobs are picked for you.
        </>
      }
      footer={
        <>
          <span className="text-muted-foreground text-sm">
            {draft.selected.length} selected
          </span>
          <div className="flex gap-2">
            <Button onClick={onSkip} variant="ghost">
              Skip
            </Button>
            <Button disabled={draft.selected.length === 0} onClick={onContinue}>
              Continue
            </Button>
          </div>
        </>
      }
      title={stepTitle.jobs}
    >
      {scan.workflows.length === 0 ? (
        <Panel className="p-6 text-center text-muted-foreground text-sm">
          No workflows in <code className="font-mono">.github/workflows</code>.
          You can still define runners for new ones.
        </Panel>
      ) : null}
      {scan.workflows.map((workflow) => {
        const keys = workflow.jobs
          .filter((job) => job.editable)
          .map((job) => jobKey(workflow.path, job.id));
        const picked = keys.filter((key) => selected.has(key)).length;
        return (
          <Panel key={workflow.path}>
            <div className="flex items-center gap-3 border-b bg-muted/50 px-4 py-2.5">
              <Checkbox
                aria-label={`Select every job in ${workflow.path}`}
                checked={keys.length > 0 && picked === keys.length}
                disabled={keys.length === 0}
                indeterminate={picked > 0 && picked < keys.length}
                onCheckedChange={(on) => toggle(keys, on === true)}
              />
              <FileCode
                aria-hidden="true"
                className="size-4 shrink-0 text-muted-foreground"
              />
              <span className="min-w-0 truncate font-mono text-[13px]">
                {workflow.path.replace(".github/workflows/", "")}
              </span>
              {workflow.name ? (
                <span className="truncate text-muted-foreground text-sm">
                  {workflow.name}
                </span>
              ) : null}
            </div>
            {workflow.problem ? (
              <p className="px-4 py-3 text-danger-foreground text-sm">
                {workflow.problem}
              </p>
            ) : (
              <ul>
                {workflow.jobs.map((job) => (
                  <JobRow
                    checked={selected.has(jobKey(workflow.path, job.id))}
                    job={job}
                    key={job.id}
                    onToggle={(on) =>
                      toggle([jobKey(workflow.path, job.id)], on)
                    }
                  />
                ))}
              </ul>
            )}
          </Panel>
        );
      })}
      {scan.skipped.length > 0 ? (
        <p className="text-muted-foreground text-xs">
          Not read:{" "}
          {scan.skipped
            .map((skip) => `${skip.path} (${skip.reason})`)
            .join(", ")}
        </p>
      ) : null}
    </StepPanel>
  );
}

function JobRow({
  checked,
  job,
  onToggle,
}: {
  checked: boolean;
  job: WorkflowJob;
  onToggle: (on: boolean) => void;
}) {
  const id = `job-${job.id}-${job.line}`;
  return (
    <li className="flex items-start gap-3 border-b px-4 py-3 last:border-b-0">
      <Checkbox
        checked={checked}
        className="mt-0.5"
        disabled={!job.editable}
        id={id}
        onCheckedChange={(on) => onToggle(on === true)}
      />
      <label
        className={cn(
          "grid min-w-0 flex-1 gap-1",
          job.editable ? "cursor-pointer" : "cursor-default"
        )}
        htmlFor={id}
      >
        <span className="flex flex-wrap items-center gap-2">
          <span className="font-medium text-sm">{job.name || job.id}</span>
          {job.name ? (
            <span className="font-mono text-muted-foreground text-xs">
              {job.id}
            </span>
          ) : null}
          {job.matrix ? <Badge variant="secondary">Matrix</Badge> : null}
        </span>
        <span className="flex flex-wrap items-center gap-1.5">
          {job.runsOn.map((label) => (
            <code
              className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-[12px] text-soft-foreground"
              key={label}
            >
              {label}
            </code>
          ))}
          <span className="text-muted-foreground text-xs">
            {job.platform ? platformLabel[job.platform] : kindLabel[job.kind]}
          </span>
        </span>
        {job.editable ? null : (
          <span className="text-muted-foreground text-xs">
            Can’t move automatically: {job.reason}
          </span>
        )}
        {job.editable && !suggested(job) && job.platform === "" ? (
          <span className="text-muted-foreground text-xs">
            Not a GitHub-hosted label; check it before moving
          </span>
        ) : null}
      </label>
    </li>
  );
}

// Step 3: set up runners.

function SetUpRunners({
  draft,
  onBack,
  onContinue,
  scan,
  update,
}: {
  draft: Draft;
  onBack: () => void;
  onContinue: () => void;
  scan: WorkflowScan;
  update: (change: (draft: Draft) => Draft) => void;
}) {
  const { org } = useOrg();
  const clouds = $api.useQuery("get", "/v1/orgs/{org}/clouds", {
    params: { path: { org: org.login } },
  });
  const connected = (clouds.data?.items ?? []).filter(
    (cloud) => cloud.status === "ok"
  );
  const defaultCloud: Cloud = connected[0]?.cloud ?? "aws";
  const jobs = useMemo(() => jobsByKey(scan), [scan]);
  const labels = useMemo(() => servedLabels(scan), [scan]);

  // Every selected job needs somewhere to run; suggest a runner for each
  // kind of machine once the connected clouds are known.
  const needsTargets = draft.selected.some(
    (key) => jobs.has(key) && !draft.targets[key]
  );
  useEffect(() => {
    if (clouds.isPending || !needsTargets) {
      return;
    }
    update((current) => fillTargets(current, jobs, defaultCloud, labels));
  }, [clouds.isPending, defaultCloud, jobs, labels, needsTargets, update]);

  const names = draft.runners.map((runner) => runner.name);
  const problems = draft.runners.map((runner, index) =>
    nameProblem(
      runner.name,
      names.filter((_, other) => other !== index)
    )
  );
  const options = [...draft.runners.map((r) => labelFor(r.name)), ...labels];
  const unused = new Set(unusedRunners(draft).map((runner) => runner.name));
  const incomplete =
    problems.some(Boolean) ||
    draft.runners.some((runner) => runner.instanceType.trim() === "");

  const setRunner = (index: number, runner: RunnerDraft) =>
    update((current) => {
      const before = current.runners[index];
      const runners = current.runners.map((item, at) =>
        at === index ? runner : item
      );
      // Renaming a runner keeps its jobs on it.
      const targets = Object.fromEntries(
        Object.entries(current.targets).map(([key, label]) => [
          key,
          before && label === labelFor(before.name)
            ? labelFor(runner.name)
            : label,
        ])
      );
      return { ...current, runners, targets };
    });

  const addRunner = () =>
    update((current) => {
      let name = "build";
      for (let n = 2; current.runners.some((r) => r.name === name); n += 1) {
        name = `build-${n}`;
      }
      const runner = runnerFor(
        { arm: false, image: "ubuntu2404", name, platform: "linux-x64" },
        defaultCloud
      );
      return { ...current, runners: [...current.runners, runner] };
    });

  return (
    <StepPanel
      description={
        <>
          Each runner becomes a definition in{" "}
          <code className="font-mono">airship.yaml</code>. Sizes start at four
          vCPUs, like GitHub’s standard runner. <DocsLink page="airship.yaml" />
        </>
      }
      footer={
        <>
          <Button onClick={onBack} variant="ghost">
            Back
          </Button>
          <div className="flex gap-2">
            <Button onClick={onContinue} variant="ghost">
              Skip
            </Button>
            <Button disabled={incomplete} onClick={onContinue}>
              Review changes
            </Button>
          </div>
        </>
      }
      title={stepTitle.runners}
    >
      {clouds.isSuccess && connected.length === 0 ? (
        <Panel className="flex flex-wrap items-center justify-between gap-3 border-warning/30 bg-warning-surface px-4 py-3 text-sm text-warning-foreground">
          No cloud is connected, so these jobs would wait. You can connect one
          before or after merging.
          <Link
            className={buttonVariants({ size: "sm", variant: "outline" })}
            href={`/${org.login}/cloud` as Route}
          >
            Connect a cloud
          </Link>
        </Panel>
      ) : null}
      {draft.runners.map((runner, index) => (
        <RunnerCard
          connected={connected}
          jobs={draft.selected
            .filter((key) => draft.targets[key] === labelFor(runner.name))
            .map((key) => jobs.get(key))
            .filter((job): job is WorkflowJob => job !== undefined)}
          // biome-ignore lint/suspicious/noArrayIndexKey: runners are renamed in place.
          key={index}
          nameProblem={problems[index] ?? null}
          onChange={(next) => setRunner(index, next)}
          onRemove={
            unused.has(runner.name)
              ? () =>
                  update((current) => ({
                    ...current,
                    runners: current.runners.filter((_, at) => at !== index),
                  }))
              : undefined
          }
          runner={runner}
        />
      ))}
      <div>
        <Button onClick={addRunner} size="sm" variant="outline">
          <Plus aria-hidden="true" />
          Add runner
        </Button>
      </div>
      {draft.selected.length > 0 ? (
        <Assignments
          draft={draft}
          jobs={jobs}
          onChange={(key, label) =>
            update((current) => ({
              ...current,
              targets: { ...current.targets, [key]: label },
            }))
          }
          options={options}
        />
      ) : null}
    </StepPanel>
  );
}

const selectClass =
  "h-9 w-full rounded-md border border-input bg-background px-2.5 text-sm outline-none transition-colors focus-visible:border-foreground/40 focus-visible:ring-[3px] focus-visible:ring-foreground/10 dark:bg-input/30";

function RunnerCard({
  connected,
  jobs,
  nameProblem: problem,
  onChange,
  onRemove,
  runner,
}: {
  connected: Schemas["Cloud"][];
  jobs: WorkflowJob[];
  nameProblem: string | null;
  onChange: (runner: RunnerDraft) => void;
  onRemove?: () => void;
  runner: RunnerDraft;
}) {
  const id = `runner-${runner.name}`;
  const region =
    connected.find((cloud) => cloud.cloud === runner.cloud)?.defaultRegion ??
    "";
  const connectedClouds = new Set(connected.map((cloud) => cloud.cloud));
  const notes = runnerNotes(runner, jobs);
  const spot = ["aws", "gcp", "azure"].includes(runner.cloud);

  return (
    <Panel>
      <div className="flex items-center gap-3 border-b px-4 py-3">
        <CloudLogo name={runner.cloud} />
        <code className="font-mono text-sm">{labelFor(runner.name)}</code>
        <span className="text-muted-foreground text-xs">
          {jobs.length === 1 ? "1 job" : `${jobs.length} jobs`}
        </span>
        {onRemove ? (
          <Button
            aria-label={`Remove ${runner.name}`}
            className="ml-auto"
            onClick={onRemove}
            size="icon-sm"
            variant="ghost"
          >
            <Trash2 />
          </Button>
        ) : null}
      </div>
      <div className="grid gap-4 p-4 sm:grid-cols-2">
        <Field data-invalid={problem ? true : undefined}>
          <FieldLabel htmlFor={`${id}-name`}>Name</FieldLabel>
          <Input
            aria-invalid={problem ? true : undefined}
            className="font-mono"
            id={`${id}-name`}
            onChange={(event) =>
              onChange({ ...runner, name: event.target.value.toLowerCase() })
            }
            spellCheck={false}
            value={runner.name}
          />
          {problem ? <FieldError>{problem}</FieldError> : null}
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-cloud`}>Cloud</FieldLabel>
          <select
            className={selectClass}
            id={`${id}-cloud`}
            onChange={(event) =>
              onChange(changeCloud(runner, event.target.value as Cloud))
            }
            value={runner.cloud}
          >
            {allClouds.map((cloud) => (
              <option key={cloud} value={cloud}>
                {cloudTitle[cloud]}
                {connectedClouds.has(cloud) ? "" : " (not connected)"}
              </option>
            ))}
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-type`}>Machine</FieldLabel>
          <Input
            className="font-mono"
            id={`${id}-type`}
            list={`${id}-shapes`}
            onChange={(event) =>
              onChange({ ...runner, instanceType: event.target.value })
            }
            spellCheck={false}
            value={runner.instanceType}
          />
          <datalist id={`${id}-shapes`}>
            {shapeSuggestions[runner.cloud].map((shape) => (
              <option key={shape} value={shape} />
            ))}
          </datalist>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-image`}>Image</FieldLabel>
          <Input
            className="font-mono"
            id={`${id}-image`}
            onChange={(event) =>
              onChange({ ...runner, machineImage: event.target.value })
            }
            placeholder="Cloud default"
            spellCheck={false}
            value={runner.machineImage}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-region`}>Region</FieldLabel>
          <Input
            className="font-mono"
            id={`${id}-region`}
            onChange={(event) =>
              onChange({ ...runner, region: event.target.value })
            }
            placeholder={region ? `${region} (default)` : "Cloud default"}
            spellCheck={false}
            value={runner.region}
          />
        </Field>
        {spot ? (
          <Field orientation="horizontal">
            <div className="grid flex-1 gap-1">
              <FieldLabel htmlFor={`${id}-spot`}>Spot capacity</FieldLabel>
              <FieldDescription>Cheaper; can be reclaimed</FieldDescription>
            </div>
            <Switch
              checked={runner.preemptible}
              id={`${id}-spot`}
              onCheckedChange={(on) => onChange({ ...runner, preemptible: on })}
            />
          </Field>
        ) : null}
      </div>
      {notes.length > 0 ? (
        <ul className="grid gap-1 border-t bg-warning-surface px-4 py-2.5 text-warning-foreground text-xs">
          {notes.map((note) => (
            <li key={note}>{note}</li>
          ))}
        </ul>
      ) : null}
    </Panel>
  );
}

function Assignments({
  draft,
  jobs,
  onChange,
  options,
}: {
  draft: Draft;
  jobs: Map<string, WorkflowJob>;
  onChange: (key: string, label: string) => void;
  options: string[];
}) {
  const unique = [...new Set(options)];
  return (
    <section aria-labelledby="assignments" className="grid gap-3">
      <h3 className="font-medium text-sm" id="assignments">
        Where each job runs
      </h3>
      <PanelList>
        {draft.selected.map((key) => {
          const job = jobs.get(key);
          if (!job) {
            return null;
          }
          const workflow = key.slice(0, key.lastIndexOf("#"));
          const id = `target-${key}`;
          return (
            <li
              className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5"
              key={key}
            >
              <label className="grid min-w-0 flex-1 gap-0.5" htmlFor={id}>
                <span className="truncate text-sm">{job.name || job.id}</span>
                <span className="truncate font-mono text-muted-foreground text-xs">
                  {workflow.replace(".github/workflows/", "")} ·{" "}
                  {job.runsOn.join(", ") || machineGroup(job).name}
                </span>
              </label>
              <select
                className={cn(
                  selectClass,
                  "w-auto min-w-48 font-mono text-[13px]"
                )}
                id={id}
                onChange={(event) => onChange(key, event.target.value)}
                value={draft.targets[key] ?? ""}
              >
                {draft.targets[key] ? null : <option value="">Choose…</option>}
                {unique.map((label) => (
                  <option key={label} value={label}>
                    {label}
                  </option>
                ))}
              </select>
            </li>
          );
        })}
      </PanelList>
    </section>
  );
}

// Step 4: review and ship.

function Review({
  draft,
  onBack,
  repo,
  repoId,
  scan,
}: {
  draft: Draft;
  onBack: () => void;
  repo: Schemas["Repository"] | undefined;
  repoId: number;
  scan: WorkflowScan;
}) {
  const { org } = useOrg();
  const plan = toPlan(draft);
  const path = { org: org.login, repo: repoId };
  const empty = plan.moves.length === 0 && plan.runners.length === 0;
  const preview = $api.useQuery(
    "post",
    "/v1/orgs/{org}/repositories/{repo}/migration/preview",
    { body: plan, params: { path } },
    { enabled: !empty, retry: false }
  );
  const open = $api.useMutation(
    "post",
    "/v1/orgs/{org}/repositories/{repo}/migration/pull-request",
    {
      onSuccess: () => clearDraft(org.login, repoId),
    }
  );
  const [activating, setActivating] = useState(false);

  if (open.data) {
    return (
      <Opened
        number={open.data.number}
        onActivate={() => setActivating(true)}
        repo={repo}
        url={open.data.url}
      >
        <ActivateDialog
          onClose={() => setActivating(false)}
          repo={activating ? (repo ?? null) : null}
        />
      </Opened>
    );
  }

  const files = preview.data?.files ?? [];
  return (
    <StepPanel
      description={
        <>
          Airship opens a pull request into{" "}
          <span className="font-mono text-foreground">
            {scan.defaultBranch}
          </span>
          . Jobs move when you merge it. <DocsLink page="Labels and runs-on" />
        </>
      }
      footer={
        <>
          <Button onClick={onBack} variant="ghost">
            Back
          </Button>
          <div className="flex flex-wrap items-center gap-2">
            {files.length > 0 ? <DownloadAll files={files} /> : null}
            <Button
              disabled={!preview.isSuccess || open.isPending}
              onClick={() => open.mutate({ body: plan, params: { path } })}
            >
              <GitPullRequest aria-hidden="true" />
              {open.isPending ? "Opening…" : "Open pull request"}
            </Button>
          </div>
        </>
      }
      title={stepTitle.review}
    >
      {empty ? (
        <Panel className="p-6 text-center text-muted-foreground text-sm">
          Nothing to change yet. Choose a job or add a runner.
        </Panel>
      ) : null}
      {preview.isPending && !empty ? <Skeleton className="h-64" /> : null}
      {preview.isError ? (
        <Panel className="border-danger/25 bg-danger-surface p-4 text-danger-foreground text-sm">
          <span role="alert">
            {errorMessage(preview.error, "Could not build the changes")}
          </span>
        </Panel>
      ) : null}
      {open.isError ? (
        <Panel className="border-warning/30 bg-warning-surface p-4 text-sm text-warning-foreground">
          <span role="alert">
            {errorMessage(open.error, "Could not open the pull request")}
          </span>
        </Panel>
      ) : null}
      {files.map((file) => (
        <FileDiff file={file} key={file.path} />
      ))}
      {repo && !repo.activated && files.length > 0 ? (
        <Panel className="flex flex-wrap items-center justify-between gap-3 px-4 py-3 text-sm">
          <span className="text-muted-foreground">
            {repo.fullName} is not activated, so its jobs will not launch yet
          </span>
          <Button
            onClick={() => setActivating(true)}
            size="sm"
            variant="outline"
          >
            Activate
          </Button>
          <ActivateDialog
            onClose={() => setActivating(false)}
            repo={activating ? repo : null}
          />
        </Panel>
      ) : null}
    </StepPanel>
  );
}

function Opened({
  children,
  number,
  onActivate,
  repo,
  url,
}: {
  children: ReactNode;
  number: number;
  onActivate: () => void;
  repo: Schemas["Repository"] | undefined;
  url: string;
}) {
  const { org } = useOrg();
  return (
    <Panel className="grid justify-items-center gap-4 px-6 py-12 text-center">
      <span className="grid size-10 place-items-center rounded-full bg-success-surface text-success-foreground">
        <Check aria-hidden="true" className="size-5" />
      </span>
      <div className="grid gap-1">
        <h2 className="font-semibold text-xl tracking-[-0.02em]">
          Pull request #{number} is open
        </h2>
        <p className="text-muted-foreground text-sm">
          Merge it and the next run uses your runners
        </p>
      </div>
      <div className="flex flex-wrap justify-center gap-2">
        <a
          className={buttonVariants()}
          href={url}
          rel="noopener noreferrer"
          target="_blank"
        >
          Review on GitHub
          <ExternalLink aria-hidden="true" />
        </a>
        {repo && !repo.activated ? (
          <Button onClick={onActivate} variant="outline">
            Activate {repo.fullName}
          </Button>
        ) : (
          <Link
            className={buttonVariants({ variant: "outline" })}
            href={`/${org.login}/jobs` as Route}
          >
            Watch jobs
          </Link>
        )}
      </div>
      {children}
    </Panel>
  );
}

type FileChange = Schemas["MigrationFileChange"];

function download(name: string, content: string) {
  const url = URL.createObjectURL(new Blob([content], { type: "text/yaml" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  URL.revokeObjectURL(url);
}

function DownloadAll({ files }: { files: FileChange[] }) {
  return (
    <Button
      onClick={() => {
        for (const file of files) {
          download(file.path.split("/").at(-1) ?? file.path, file.after);
        }
      }}
      variant="outline"
    >
      <Download aria-hidden="true" />
      Download files
    </Button>
  );
}

function FileDiff({ file }: { file: FileChange }) {
  const lines = useMemo(
    () => diffLines(file.before, file.after),
    [file.after, file.before]
  );
  const added = lines.filter((line) => line.kind === "add").length;
  const removed = lines.filter((line) => line.kind === "remove").length;
  const shown = hunks(lines);

  return (
    <figure className="overflow-hidden rounded-lg border bg-card">
      <figcaption className="flex h-10 items-center gap-2 border-b bg-muted pr-1.5 pl-4">
        <FileCode
          aria-hidden="true"
          className="size-3.5 shrink-0 text-muted-foreground"
        />
        <span className="truncate font-mono text-xs">{file.path}</span>
        {file.created ? <Badge variant="success">New</Badge> : null}
        <span className="figure text-success-foreground text-xs">+{added}</span>
        <span className="figure text-danger-foreground text-xs">
          −{removed}
        </span>
        <span className="ml-auto flex">
          <Button
            aria-label={`Download ${file.path}`}
            onClick={() =>
              download(file.path.split("/").at(-1) ?? file.path, file.after)
            }
            size="icon-sm"
            variant="ghost"
          >
            <Download />
          </Button>
          <CopyButton label={`Copy ${file.path}`} value={file.after} />
        </span>
      </figcaption>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse font-mono text-[12.5px]/[20px]">
          <tbody>
            {shown.map((hunk, index) =>
              hunk.kind === "fold" ? (
                // biome-ignore lint/suspicious/noArrayIndexKey: hunks have no identity.
                <tr className="bg-muted/50 text-muted-foreground" key={index}>
                  <td className="select-none px-3 py-1 text-xs" colSpan={3}>
                    {hunk.count} unchanged {hunk.count === 1 ? "line" : "lines"}
                  </td>
                </tr>
              ) : (
                hunk.lines.map((line) => (
                  <tr
                    className={cn(
                      line.kind === "add" && "bg-success-surface",
                      line.kind === "remove" && "bg-danger-surface"
                    )}
                    key={`${line.kind}-${line.before ?? ""}-${line.after ?? ""}`}
                  >
                    <td className="w-10 select-none px-2 text-right text-faint">
                      {line.before ?? ""}
                    </td>
                    <td className="w-10 select-none px-2 text-right text-faint">
                      {line.after ?? ""}
                    </td>
                    <td
                      className={cn(
                        "whitespace-pre pr-4",
                        line.kind === "add" && "text-success-foreground",
                        line.kind === "remove" && "text-danger-foreground"
                      )}
                    >
                      <span aria-hidden="true" className="select-none">
                        {line.kind === "add" ? "+ " : ""}
                        {line.kind === "remove" ? "- " : ""}
                        {line.kind === "keep" ? "  " : ""}
                      </span>
                      {line.text}
                    </td>
                  </tr>
                ))
              )
            )}
          </tbody>
        </table>
      </div>
    </figure>
  );
}
