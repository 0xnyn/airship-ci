"use client";

import type { Schemas } from "@airship/client";
import { Button, buttonVariants } from "@airship/ui/components/button";
import { Kbd } from "@airship/ui/components/kbd";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import {
  ExternalLink,
  GitBranch,
  Globe,
  Lock,
  RefreshCw,
  Search,
} from "lucide-react";
import { type ReactNode, useState } from "react";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { matchRepositories } from "@/lib/repository-name";

import { useOrg } from "./org-context";

const REPOSITORIES = "/v1/orgs/{org}/repositories";

/**
 * Every repository the GitHub App can see, searchable, with a name, an
 * owner/name or a GitHub URL pasted straight in. A repository the app
 * cannot see gets a way to grant access, and the list refreshes after.
 */
export function RepositoryPicker({
  action,
  badges,
  onPick,
  pickable = () => true,
  sort,
}: {
  /** The row's control; defaults to a Select button. */
  action?: (repo: Schemas["Repository"]) => ReactNode;
  badges?: (repo: Schemas["Repository"]) => ReactNode;
  onPick?: (repo: Schemas["Repository"]) => void;
  pickable?: (repo: Schemas["Repository"]) => boolean;
  sort?: (a: Schemas["Repository"], b: Schemas["Repository"]) => number;
}) {
  const { org } = useOrg();
  const [query, setQuery] = useState("");
  const config = $api.useQuery("get", "/v1/auth/config");
  const repos = $api.useQuery("get", REPOSITORIES, {
    params: { path: { org: org.login } },
  });
  const installUrl = config.data?.installUrl;

  const visible = (repos.data?.items ?? []).filter((repo) => repo.accessible);
  const found = matchRepositories(visible, query, org.login);
  const items = found.items.sort(
    sort ?? ((a, b) => a.fullName.localeCompare(b.fullName))
  );
  const firstPickable =
    onPick && query.trim() !== "" ? items.find(pickable) : undefined;

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <div className="flex items-center gap-2 border-b p-2">
        <div className="relative flex-1 rounded-md focus-within:bg-muted/50">
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
          />
          <input
            aria-label="Search repositories, or paste a name or GitHub URL"
            autoComplete="off"
            autoFocus
            className="h-9 w-full rounded-md bg-transparent pr-16 pl-9 text-sm outline-none placeholder:text-muted-foreground"
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && firstPickable) {
                event.preventDefault();
                onPick?.(firstPickable);
              }
            }}
            placeholder="Search or paste a GitHub URL"
            spellCheck={false}
            value={query}
          />
          {firstPickable ? (
            <Kbd className="absolute top-1/2 right-2 -translate-y-1/2">↵</Kbd>
          ) : null}
        </div>
        <Button
          aria-label="Refresh the list"
          className="text-muted-foreground"
          disabled={repos.isFetching}
          onClick={() => repos.refetch()}
          size="icon-sm"
          title="Refresh after changing access on GitHub"
          variant="ghost"
        >
          <RefreshCw className={cn(repos.isFetching && "animate-spin")} />
        </Button>
      </div>

      <Results
        action={action}
        badges={badges}
        installUrl={installUrl}
        items={items}
        missing={found.missing}
        onPick={onPick}
        pickable={pickable}
        repos={repos}
        visible={visible.length}
      />
      {repos.isSuccess && items.length > 0 && installUrl ? (
        <div className="flex items-center justify-between gap-3 border-t bg-muted/60 px-4 py-2.5 text-muted-foreground text-xs">
          <span className="hidden sm:inline">
            {visible.length === 1
              ? "1 repository visible"
              : `${visible.length} repositories visible`}
          </span>
          <a
            className="inline-flex items-center gap-1 font-medium text-foreground underline-offset-4 hover:underline"
            href={installUrl}
            rel="noopener noreferrer"
            target="_blank"
          >
            Missing one? Adjust access on GitHub
            <ExternalLink aria-hidden="true" className="size-3" />
          </a>
        </div>
      ) : null}
    </div>
  );
}

function Results({
  action,
  badges,
  installUrl,
  items,
  missing,
  onPick,
  pickable,
  repos,
  visible,
}: {
  action?: (repo: Schemas["Repository"]) => ReactNode;
  badges?: (repo: Schemas["Repository"]) => ReactNode;
  installUrl: string | null | undefined;
  items: Schemas["Repository"][];
  missing: string | null;
  onPick?: (repo: Schemas["Repository"]) => void;
  pickable: (repo: Schemas["Repository"]) => boolean;
  repos: { error: unknown; isError: boolean; isPending: boolean };
  visible: number;
}) {
  if (repos.isPending) {
    return (
      <div className="divide-y">
        {["a", "b", "c", "d"].map((key) => (
          <div className="flex items-center gap-3 px-4 py-3" key={key}>
            <Skeleton className="size-8 rounded-md" />
            <div className="grid gap-1.5">
              <Skeleton className="h-3.5 w-44" />
              <Skeleton className="h-3 w-16" />
            </div>
            <Skeleton className="ml-auto h-8 w-16" />
          </div>
        ))}
      </div>
    );
  }
  if (repos.isError) {
    return (
      <p className="px-4 py-6 text-danger-foreground text-sm" role="alert">
        {errorMessage(repos.error, "Could not list repositories")}
      </p>
    );
  }
  if (items.length === 0) {
    const nothingVisible = visible === 0;
    let message = missing ?? "No repository matches";
    if (nothingVisible && !missing) {
      message = "Airship cannot see any repositories yet";
    }
    return (
      <div className="grid justify-items-center gap-3 px-6 py-10 text-center">
        <p className="text-muted-foreground text-sm">{message}</p>
        {installUrl && (missing || nothingVisible) ? (
          <GrantAccess href={installUrl} />
        ) : null}
      </div>
    );
  }
  return (
    <ul className="max-h-[440px] divide-y overflow-y-auto">
      {items.map((repo) => (
        <Row
          action={action}
          badges={badges}
          key={repo.id}
          onPick={onPick}
          pickable={pickable(repo)}
          repo={repo}
        />
      ))}
    </ul>
  );
}

function GrantAccess({ href }: { href: string }) {
  return (
    <a
      className={buttonVariants({ size: "sm", variant: "outline" })}
      href={href}
      rel="noopener noreferrer"
      target="_blank"
    >
      Grant access on GitHub
      <ExternalLink aria-hidden="true" />
    </a>
  );
}

function Row({
  action,
  badges,
  onPick,
  pickable,
  repo,
}: {
  action?: (repo: Schemas["Repository"]) => ReactNode;
  badges?: (repo: Schemas["Repository"]) => ReactNode;
  onPick?: (repo: Schemas["Repository"]) => void;
  pickable: boolean;
  repo: Schemas["Repository"];
}) {
  const [owner, name] = repo.fullName.split("/");
  const Icon = repo.private ? Lock : Globe;
  return (
    <li className="group/row flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/50">
      <span className="grid size-8 shrink-0 place-items-center rounded-md border bg-background text-muted-foreground">
        <Icon
          aria-label={repo.private ? "Private" : "Public"}
          className="size-3.5"
        />
      </span>
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm">
            <span className="text-muted-foreground">{owner}/</span>
            <span className="font-medium">{name}</span>
          </span>
          {badges?.(repo)}
        </span>
        <span className="flex items-center gap-1 font-mono text-muted-foreground text-xs">
          <GitBranch aria-hidden="true" className="size-3" />
          {repo.defaultBranch}
        </span>
      </span>
      {action ? action(repo) : null}
      {!action && pickable ? (
        <Button onClick={() => onPick?.(repo)} size="sm" variant="outline">
          Select
        </Button>
      ) : null}
    </li>
  );
}
