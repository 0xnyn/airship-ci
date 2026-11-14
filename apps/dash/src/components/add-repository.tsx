"use client";

import type { Schemas } from "@airship/client";
import { Badge } from "@airship/ui/components/badge";
import { Button, buttonVariants } from "@airship/ui/components/button";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@airship/ui/components/field";
import { Switch } from "@airship/ui/components/switch";
import { cn } from "@airship/ui/lib/utils";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowRight,
  CircleCheck,
  FileCode,
  GitPullRequest,
  Globe,
  Lock,
} from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { DocsLink } from "./docs-link";
import { OnboardingLayout, StepPanel, StepSkeleton } from "./onboarding";
import { useOrg } from "./org-context";
import { Fact, Facts, Panel } from "./panel";
import { RepositoryPicker } from "./repository-picker";

const REPOSITORIES = "/v1/orgs/{org}/repositories";
const ACTIVATION = "/v1/orgs/{org}/repositories/{repo}/activation";
const POOLS = "/v1/orgs/{org}/pools";
const SHA_PREFIX = 7;

const STEPS = ["repository", "options", "activate", "workflows"] as const;
type Step = (typeof STEPS)[number];

const copy: Record<Step, { hint: string; title: string }> = {
  activate: { hint: "Review and turn it on", title: "Activate" },
  options: { hint: "Optional", title: "Configure" },
  repository: { hint: "Any repository the app can see", title: "Repository" },
  workflows: { hint: "Optional", title: "Move workflows" },
};

function isStep(value: string | null): value is Step {
  return STEPS.includes(value as Step);
}

/** Adding a repository: pick it, choose its options, activate, then move jobs. */
export function AddRepository() {
  const { isAdmin, org } = useOrg();
  const router = useRouter();
  const params = useSearchParams();
  const repos = $api.useQuery("get", REPOSITORIES, {
    params: { path: { org: org.login } },
  });
  const [allowPublic, setAllowPublic] = useState(false);

  const repoId = Number(params.get("repo")) || null;
  const repo = (repos.data?.items ?? []).find((item) => item.id === repoId);
  const asked = params.get("step");
  let step: Step = isStep(asked) ? asked : "repository";
  if (!repo) {
    step = "repository";
  } else if (step === "workflows" && !repo.activated) {
    step = "activate";
  }

  const go = (next: Step, id: number | null = repoId) => {
    const query = new URLSearchParams();
    if (id !== null) {
      query.set("repo", String(id));
    }
    query.set("step", next);
    router.replace(`/${org.login}/repositories/new?${query}` as Route, {
      scroll: false,
    });
  };
  const back = { href: `/${org.login}/repositories`, label: "Repositories" };

  const index = STEPS.indexOf(step);
  const steps = STEPS.map((id, position) => ({
    disabled:
      !isAdmin ||
      (id !== "repository" && !repo) ||
      (id === "workflows" && !repo?.activated),
    done:
      (id === "repository" && repo !== undefined) ||
      (id === "activate" && repo?.activated === true) ||
      (id === "options" && position < index),
    hint: id === "repository" && repo ? repo.fullName : copy[id].hint,
    id,
    title: copy[id].title,
  }));

  return (
    <OnboardingLayout
      back={back}
      current={step}
      description="Let a repository launch runners in your cloud"
      onVisit={(next) => go(next)}
      steps={steps}
      title="Add repository"
    >
      {isAdmin ? null : (
        <Panel className="p-6 text-muted-foreground text-sm">
          Only organization owners can add repositories
        </Panel>
      )}
      {isAdmin && repoId !== null && repos.isPending ? <StepSkeleton /> : null}
      {isAdmin && step === "repository" && !(repoId && repos.isPending) ? (
        <ChooseStep onPick={(picked) => go("options", picked.id)} />
      ) : null}
      {isAdmin && repo && step === "options" ? (
        <OptionsStep
          allowPublic={allowPublic}
          onBack={() => go("repository", null)}
          onChange={setAllowPublic}
          onNext={() => go("activate")}
          repo={repo}
        />
      ) : null}
      {isAdmin && repo && step === "activate" ? (
        <ActivateStep
          allowPublic={allowPublic}
          onActivated={() => go("workflows")}
          onBack={() => go("options")}
          repo={repo}
        />
      ) : null}
      {isAdmin && repo && step === "workflows" ? (
        <WorkflowsStep
          onAnother={() => {
            setAllowPublic(false);
            go("repository", null);
          }}
          repo={repo}
        />
      ) : null}
    </OnboardingLayout>
  );
}

function ChooseStep({
  onPick,
}: {
  onPick: (repo: Schemas["Repository"]) => void;
}) {
  return (
    <StepPanel
      description="Search the repositories the GitHub App can see, or paste a name or URL"
      title="Choose a repository"
    >
      <RepositoryPicker
        badges={(repo) =>
          repo.activated ? <Badge variant="success">Activated</Badge> : null
        }
        onPick={onPick}
        pickable={(repo) => !repo.activated}
        sort={(a, b) =>
          Number(a.activated) - Number(b.activated) ||
          a.fullName.localeCompare(b.fullName)
        }
      />
    </StepPanel>
  );
}

function Footer({
  children,
  onBack,
}: {
  children: ReactNode;
  onBack?: () => void;
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
      <div className="flex gap-2">{children}</div>
    </>
  );
}

function OptionsStep({
  allowPublic,
  onBack,
  onChange,
  onNext,
  repo,
}: {
  allowPublic: boolean;
  onBack: () => void;
  onChange: (value: boolean) => void;
  onNext: () => void;
  repo: Schemas["Repository"];
}) {
  return (
    <StepPanel
      description="The defaults are safe; skip this if you are unsure"
      footer={
        <Footer onBack={onBack}>
          <Button
            onClick={() => {
              onChange(false);
              onNext();
            }}
            variant="ghost"
          >
            Skip
          </Button>
          <Button onClick={onNext}>
            Continue
            <ArrowRight aria-hidden="true" />
          </Button>
        </Footer>
      }
      title="Configure"
    >
      <Panel className="divide-y">
        <Field className="p-5" orientation="horizontal">
          <FieldContent>
            <FieldLabel htmlFor="allow-public">
              Run jobs while public
            </FieldLabel>
            <FieldDescription>
              {repo.private
                ? "If this repository becomes public, Airship stops its jobs unless this is on"
                : "This repository is public: anyone who opens a pull request can run code on your machines"}
            </FieldDescription>
          </FieldContent>
          <Switch
            checked={allowPublic}
            id="allow-public"
            onCheckedChange={onChange}
          />
        </Field>
        <div className="flex items-center justify-between gap-4 px-5 py-4 text-sm">
          <span className="grid gap-0.5">
            <span className="font-medium">Runners</span>
            <span className="text-muted-foreground">
              Defined in the repository&apos;s airship.yaml, or by a one-line
              label
            </span>
          </span>
          <DocsLink page="airship.yaml" />
        </div>
      </Panel>
    </StepPanel>
  );
}

function ActivateStep({
  allowPublic,
  onActivated,
  onBack,
  repo,
}: {
  allowPublic: boolean;
  onActivated: () => void;
  onBack: () => void;
  repo: Schemas["Repository"];
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const activate = $api.useMutation("put", ACTIVATION, {
    onError: (error) => {
      toast.error(errorMessage(error, "Could not activate the repository"));
    },
    onSuccess: async (activated) => {
      toast.success(
        activated.config?.error
          ? `Activated ${activated.fullName}, but its airship.yaml has a problem`
          : `Activated ${activated.fullName}`
      );
      await invalidatePath(queryClient, REPOSITORIES, POOLS);
      onActivated();
    },
  });
  const VisibilityIcon = repo.private ? Lock : Globe;

  return (
    <StepPanel
      description={
        <>
          Jobs asking for an <code className="font-mono">airship-</code> label
          launch machines in your cloud, and idle pools start right away.{" "}
          <DocsLink page="Security and trust" />
        </>
      }
      footer={
        <Footer onBack={onBack}>
          {repo.activated ? (
            <Button onClick={onActivated}>
              Continue
              <ArrowRight aria-hidden="true" />
            </Button>
          ) : (
            <Button
              disabled={activate.isPending}
              onClick={() =>
                activate.mutate({
                  body: { allowPublic },
                  params: { path: { org: org.login, repo: repo.id } },
                })
              }
            >
              {activate.isPending ? "Activating…" : "Activate"}
            </Button>
          )}
        </Footer>
      }
      title={repo.activated ? "Already activated" : "Review and activate"}
    >
      <Panel className="p-5">
        <Facts className="lg:grid-cols-4">
          <Fact label="Repository">{repo.fullName}</Fact>
          <Fact label="Visibility">
            <span className="inline-flex items-center gap-1.5">
              <VisibilityIcon
                aria-hidden="true"
                className="size-3.5 text-muted-foreground"
              />
              {repo.private ? "Private" : "Public"}
            </span>
          </Fact>
          <Fact label="Default branch" mono>
            {repo.defaultBranch}
          </Fact>
          <Fact label="Runs while public">
            {(repo.activated ? repo.allowPublic : allowPublic) ? "Yes" : "No"}
          </Fact>
        </Facts>
      </Panel>
    </StepPanel>
  );
}

function WorkflowsStep({
  onAnother,
  repo,
}: {
  onAnother: () => void;
  repo: Schemas["Repository"];
}) {
  const { org } = useOrg();
  return (
    <StepPanel
      description="Point jobs at your runners. Nothing changes until you merge."
      footer={
        <Footer>
          <Button onClick={onAnother} variant="ghost">
            Add another
          </Button>
          <Link
            className={buttonVariants({ variant: "outline" })}
            href={`/${org.login}/repositories` as Route}
          >
            Done
          </Link>
        </Footer>
      }
      title="Move workflows"
    >
      <Panel className="flex items-start gap-3 p-5">
        <CircleCheck
          aria-hidden="true"
          className="mt-0.5 size-5 shrink-0 text-success"
        />
        <div className="grid min-w-0 gap-1 text-sm">
          <p className="font-medium">{repo.fullName} is active</p>
          <p className="text-muted-foreground">
            <ConfigLine repo={repo} />
          </p>
        </div>
      </Panel>
      <div className="grid gap-3 sm:grid-cols-2">
        <Choice
          description="Airship reads your workflows, suggests runners and opens a pull request"
          href={`/${org.login}/repositories/migrate?repo=${repo.id}`}
          icon={GitPullRequest}
          primary
          title="Move workflows"
        />
        <Choice
          description="Write airship.yaml yourself and check it before you commit"
          href={`/${org.login}/repositories#check-config`}
          icon={FileCode}
          title="Write airship.yaml"
        />
      </div>
    </StepPanel>
  );
}

function ConfigLine({ repo }: { repo: Schemas["Repository"] }) {
  const { config } = repo;
  if (config?.error) {
    return (
      <span className="text-danger-foreground">
        airship.yaml has a problem: {config.error}
      </span>
    );
  }
  if (config?.sha) {
    return (
      <>
        Read airship.yaml at{" "}
        <code className="font-mono text-xs">
          {config.sha.slice(0, SHA_PREFIX)}
        </code>
      </>
    );
  }
  return <>No airship.yaml yet; one-line labels already work</>;
}

function Choice({
  description,
  href,
  icon: Icon,
  primary = false,
  title,
}: {
  description: string;
  href: string;
  icon: typeof FileCode;
  primary?: boolean;
  title: string;
}) {
  return (
    <Link
      className={cn(
        "group grid gap-3 rounded-lg border bg-card p-5 outline-none transition-colors hover:border-foreground/30 hover:bg-muted/40 focus-visible:ring-1 focus-visible:ring-ring",
        primary && "border-foreground/20"
      )}
      href={href as Route}
    >
      <span className="grid size-9 place-items-center rounded-md border bg-background text-muted-foreground transition-colors group-hover:text-foreground">
        <Icon aria-hidden="true" className="size-4" />
      </span>
      <span className="grid gap-1">
        <span className="flex items-center gap-1.5 font-medium text-sm">
          {title}
          <ArrowRight
            aria-hidden="true"
            className="size-3.5 -translate-x-1 opacity-0 transition-all group-hover:translate-x-0 group-hover:opacity-100"
          />
        </span>
        <span className="text-pretty text-muted-foreground text-sm">
          {description}
        </span>
      </span>
    </Link>
  );
}
