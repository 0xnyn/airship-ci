"use client";

import type { Schemas } from "@airship/client";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@airship/ui/components/alert-dialog";
import { Badge } from "@airship/ui/components/badge";
import { Button, buttonVariants } from "@airship/ui/components/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@airship/ui/components/dropdown-menu";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@airship/ui/components/field";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@airship/ui/components/sheet";
import { Switch } from "@airship/ui/components/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { useQueryClient } from "@tanstack/react-query";
import {
  ArrowUpRight,
  GitBranch,
  GitPullRequest,
  Globe,
  Lock,
  MoreHorizontal,
  PanelRight,
  Plus,
  Power,
  Search,
} from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import {
  ClickableRow,
  EmptyRow,
  FilterTabs,
  Pagination,
  TableSkeleton,
  useClientPages,
  useDetailParam,
} from "./data-table";
import { useOrg } from "./org-context";
import { DetailSection, Fact, Facts } from "./panel";
import { StatusDot } from "./status";
import { RelativeTime } from "./time";

const POLL_MS = 15_000;
const REPOSITORIES = "/v1/orgs/{org}/repositories";
const ACTIVATION = "/v1/orgs/{org}/repositories/{repo}/activation";
const POOLS = "/v1/orgs/{org}/pools";
const SHA_PREFIX = 7;

type Repository = Schemas["Repository"];
type Filter = "all" | "active" | "inactive";

const filters = [
  { label: "All", value: "all" },
  { label: "Active", value: "active" },
  { label: "Not active", value: "inactive" },
] as const satisfies readonly { label: string; value: Filter }[];

function matches(repo: Repository, filter: Filter, needle: string): boolean {
  if (filter === "active" && !repo.activated) {
    return false;
  }
  if (filter === "inactive" && repo.activated) {
    return false;
  }
  return needle === "" || repo.fullName.toLowerCase().includes(needle);
}

/** Every repository the app can see, activated or not, with its actions. */
export function RepositoryTable() {
  const { isAdmin, org } = useOrg();
  const repos = $api.useQuery(
    "get",
    REPOSITORIES,
    { params: { path: { org: org.login } } },
    { refetchInterval: POLL_MS }
  );
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const detail = useDetailParam();
  const all = repos.data?.items ?? [];
  const needle = query.trim().toLowerCase();
  const visible = all
    .filter((repo) => matches(repo, filter, needle))
    .sort(
      (a, b) =>
        Number(b.activated) - Number(a.activated) ||
        a.fullName.localeCompare(b.fullName)
    );
  const { pagination, rows } = useClientPages(visible, `${filter}:${needle}`);
  const open = all.find((repo) => String(repo.id) === detail.value) ?? null;

  if (repos.isPending) {
    return <TableSkeleton />;
  }
  if (repos.isError) {
    return (
      <EmptyRow>
        {errorMessage(repos.error, "Could not list repositories")}
      </EmptyRow>
    );
  }
  if (all.length === 0) {
    return (
      <EmptyRow>
        The app cannot see any repositories yet. Grant it access on GitHub.
      </EmptyRow>
    );
  }

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <FilterTabs
          label="Repositories"
          onChange={setFilter}
          options={filters}
          value={filter}
        />
        <div className="relative w-full sm:w-64">
          <Search
            aria-hidden="true"
            className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
          />
          <input
            aria-label="Search repositories"
            className="h-8 w-full rounded-md border bg-background pr-2 pl-8 text-[13px] outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search…"
            value={query}
          />
        </div>
      </div>
      {visible.length === 0 ? (
        <EmptyRow
          action={
            isAdmin && filter !== "inactive" ? (
              <Button
                nativeButton={false}
                render={
                  <Link href={`/${org.login}/repositories/new` as Route} />
                }
                size="sm"
                variant="outline"
              >
                <Plus />
                Add repository
              </Button>
            ) : null
          }
        >
          {needle
            ? "No repository matches"
            : `No ${filter === "active" ? "active" : "inactive"} repositories`}
        </EmptyRow>
      ) : (
        <Table className="text-sm" footer={<Pagination {...pagination} />}>
          <TableHeader>
            <TableRow>
              <TableHead>Repository</TableHead>
              <TableHead className="hidden sm:table-cell">Status</TableHead>
              <TableHead className="hidden md:table-cell">
                airship.yaml
              </TableHead>
              <TableHead className="hidden text-right lg:table-cell">
                Checked
              </TableHead>
              <TableHead className="w-0">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((repo) => (
              <ClickableRow
                key={repo.id}
                label={`${repo.fullName} details`}
                onOpen={() => detail.open(String(repo.id))}
                selected={open?.id === repo.id}
              >
                <TableCell className="w-full max-w-0 sm:w-auto sm:max-w-72">
                  <RepositoryName repo={repo} />
                </TableCell>
                <TableCell className="hidden sm:table-cell">
                  <RepositoryStatus repo={repo} />
                </TableCell>
                <TableCell className="hidden max-w-80 md:table-cell">
                  <ConfigStatus repo={repo} />
                </TableCell>
                <TableCell className="hidden text-right text-muted-foreground lg:table-cell">
                  {repo.config?.checkedAt ? (
                    <RelativeTime value={repo.config.checkedAt} />
                  ) : (
                    "—"
                  )}
                </TableCell>
                <TableCell className="py-1.5 text-right">
                  <RowMenu
                    onDetails={() => detail.open(String(repo.id))}
                    repo={repo}
                  />
                </TableCell>
              </ClickableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <RepositorySheet onClose={detail.close} repo={open} />
    </div>
  );
}

function RepositoryName({ repo }: { repo: Repository }) {
  const [owner, name] = repo.fullName.split("/");
  const Icon = repo.private ? Lock : Globe;
  return (
    <div className="flex min-w-0 items-center gap-3">
      <span className="grid size-7 shrink-0 place-items-center rounded-md border bg-background text-muted-foreground">
        <Icon
          aria-label={repo.private ? "Private" : "Public"}
          className="size-3.5"
        />
      </span>
      <span className="grid min-w-0 gap-0.5">
        <span className="truncate">
          <span className="hidden text-muted-foreground sm:inline">
            {owner}/
          </span>
          <span className="font-medium">{name}</span>
        </span>
        <span className="text-xs sm:hidden">
          <RepositoryStatus repo={repo} />
        </span>
        <span className="hidden items-center gap-1 font-mono text-muted-foreground text-xs sm:flex">
          <GitBranch aria-hidden="true" className="size-3" />
          {repo.defaultBranch}
        </span>
      </span>
    </div>
  );
}

export function RepositoryStatus({ repo }: { repo: Repository }) {
  if (!repo.accessible) {
    return <StatusDot label="App lost access" tone="danger" />;
  }
  if (!repo.activated) {
    return <span className="text-muted-foreground">Not active</span>;
  }
  if (!(repo.private || repo.allowPublic)) {
    return <StatusDot label="Paused while public" tone="warning" />;
  }
  if (repo.config?.error) {
    return <StatusDot label="Config problem" tone="warning" />;
  }
  return <StatusDot label="Active" tone="success" />;
}

function ConfigStatus({ repo }: { repo: Repository }) {
  const { config } = repo;
  if (!repo.activated) {
    return <span className="text-muted-foreground">—</span>;
  }
  if (!config?.checkedAt) {
    return <span className="text-muted-foreground">Not read yet</span>;
  }
  if (config.error) {
    return (
      <span
        className="block truncate text-danger-foreground"
        title={config.error}
      >
        {config.error}
      </span>
    );
  }
  if (!config.sha) {
    return (
      <span className="text-muted-foreground">
        None, one-line labels still work
      </span>
    );
  }
  return (
    <span className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-xs">
      {config.sha.slice(0, SHA_PREFIX)}
    </span>
  );
}

const activateHref = (org: string, repo: Repository) =>
  `/${org}/repositories/new?repo=${repo.id}&step=options` as Route;
const migrateHref = (org: string, repo: Repository) =>
  `/${org}/repositories/migrate?repo=${repo.id}` as Route;

/** Activates, updates or deactivates a repository, with toasts. */
function useActivation(repo: Repository) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const refresh = () => invalidatePath(queryClient, REPOSITORIES, POOLS);
  const update = $api.useMutation("put", ACTIVATION, {
    onError: (error) => {
      toast.error(errorMessage(error, "Could not update the repository"));
    },
    onSuccess: async (saved) => {
      toast.success(
        saved.allowPublic
          ? `${saved.fullName} now runs jobs while public`
          : `${saved.fullName} stops jobs while public`
      );
      await refresh();
    },
  });
  const deactivate = $api.useMutation("delete", ACTIVATION, {
    onError: (error) => {
      toast.error(errorMessage(error, "Could not deactivate the repository"));
    },
    onSuccess: async () => {
      toast.success(`Deactivated ${repo.fullName}`);
      await refresh();
    },
  });
  const path = { params: { path: { org: org.login, repo: repo.id } } };
  return {
    deactivate: () => deactivate.mutateAsync(path),
    deactivating: deactivate.isPending,
    setAllowPublic: (allowPublic: boolean) =>
      update.mutate({ ...path, body: { allowPublic } }),
    updating: update.isPending,
  };
}

function RowMenu({
  onDetails,
  repo,
}: {
  onDetails: () => void;
  repo: Repository;
}) {
  const { isAdmin, org } = useOrg();
  const router = useRouter();
  const activation = useActivation(repo);
  const [confirming, setConfirming] = useState(false);

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          className={buttonVariants({ size: "icon-sm", variant: "ghost" })}
        >
          <MoreHorizontal />
          <span className="sr-only">Actions for {repo.fullName}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-60">
          {isAdmin && !repo.activated ? (
            <DropdownMenuItem
              onClick={() => router.push(activateHref(org.login, repo))}
            >
              <Power />
              Activate
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onClick={onDetails}>
            <PanelRight />
            View details
          </DropdownMenuItem>
          {isAdmin ? (
            <DropdownMenuItem
              onClick={() => router.push(migrateHref(org.login, repo))}
            >
              <GitPullRequest />
              Move workflows
            </DropdownMenuItem>
          ) : null}
          {isAdmin && repo.activated ? (
            <DropdownMenuCheckboxItem
              checked={repo.allowPublic}
              disabled={activation.updating}
              onCheckedChange={(checked) => activation.setAllowPublic(checked)}
            >
              <Globe />
              Run jobs while public
            </DropdownMenuCheckboxItem>
          ) : null}
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() =>
              window.open(
                `https://github.com/${repo.fullName}`,
                "_blank",
                "noopener"
              )
            }
          >
            <ArrowUpRight />
            Open on GitHub
          </DropdownMenuItem>
          {isAdmin && repo.activated ? (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onClick={() => setConfirming(true)}
                variant="destructive"
              >
                <Power />
                Deactivate
              </DropdownMenuItem>
            </>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>
      <DeactivateDialog
        activation={activation}
        onOpenChange={setConfirming}
        open={confirming}
        repo={repo}
      />
    </>
  );
}

function DeactivateDialog({
  activation,
  onOpenChange,
  open,
  repo,
}: {
  activation: ReturnType<typeof useActivation>;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  repo: Repository;
}) {
  return (
    <AlertDialog onOpenChange={onOpenChange} open={open}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Deactivate {repo.fullName}?</AlertDialogTitle>
          <AlertDialogDescription>
            New jobs stop getting runners and its idle pools drain. Live runners
            finish first.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction
            disabled={activation.deactivating}
            onClick={async () => {
              await activation.deactivate().catch(() => null);
              onOpenChange(false);
            }}
            variant="destructive"
          >
            Deactivate
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

/** A repository's detail, sliding in from the right. */
function RepositorySheet({
  onClose,
  repo,
}: {
  onClose: () => void;
  repo: Repository | null;
}) {
  return (
    <Sheet
      onOpenChange={(next) => (next ? null : onClose())}
      open={repo !== null}
    >
      <SheetContent>
        {repo ? <RepositoryDetail repo={repo} /> : null}
      </SheetContent>
    </Sheet>
  );
}

function RepositoryDetail({ repo }: { repo: Repository }) {
  const { isAdmin, org } = useOrg();
  const activation = useActivation(repo);
  const [confirming, setConfirming] = useState(false);
  const pools = $api.useQuery("get", POOLS, {
    params: { path: { org: org.login } },
  });
  const ownPools = (pools.data?.items ?? []).filter(
    (pool) => pool.repository.toLowerCase() === repo.fullName.toLowerCase()
  );

  return (
    <>
      <SheetHeader
        actions={
          <a
            aria-label="Open on GitHub"
            className={buttonVariants({ size: "icon-sm", variant: "ghost" })}
            href={`https://github.com/${repo.fullName}`}
            rel="noopener noreferrer"
            target="_blank"
            title="Open on GitHub"
          >
            <ArrowUpRight />
          </a>
        }
      >
        <SheetTitle>{repo.fullName}</SheetTitle>
        <SheetDescription className="flex items-center gap-2">
          <RepositoryStatus repo={repo} />
          {repo.private ? null : <Badge variant="outline">Public</Badge>}
        </SheetDescription>
      </SheetHeader>
      <SheetBody className="grid content-start gap-6">
        <Facts className="grid-cols-2 lg:grid-cols-2">
          <Fact label="Default branch" mono>
            {repo.defaultBranch}
          </Fact>
          <Fact label="Visibility">{repo.private ? "Private" : "Public"}</Fact>
          <Fact label="Activated">
            {repo.activatedAt ? (
              <RelativeTime value={repo.activatedAt} />
            ) : (
              "Not active"
            )}
          </Fact>
          <Fact label="Runs while public">
            {repo.allowPublic ? "Yes" : "No"}
          </Fact>
        </Facts>
        {repo.activated ? (
          <DetailSection title="airship.yaml">
            <ConfigDetail repo={repo} />
          </DetailSection>
        ) : null}
        {repo.activated ? (
          <DetailSection title="Idle pools">
            {ownPools.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No idle pools in this repository
              </p>
            ) : (
              <ul className="divide-y rounded-lg border">
                {ownPools.map((pool) => (
                  <li
                    className="flex items-center justify-between gap-3 px-3 py-2 text-sm"
                    key={pool.id}
                  >
                    <span className="font-mono text-[13px]">{pool.name}</span>
                    <span className="text-muted-foreground tabular-nums">
                      {pool.idle} idle of {pool.desired}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </DetailSection>
        ) : null}
        {repo.activated && isAdmin ? (
          <DetailSection title="Public repositories">
            <Field className="rounded-lg border p-3" orientation="horizontal">
              <FieldContent>
                <FieldLabel htmlFor="detail-allow-public">
                  Run jobs while public
                </FieldLabel>
                <FieldDescription>
                  {repo.private
                    ? "If it becomes public, jobs stop unless this is on"
                    : "Anyone who opens a pull request can run code on your machines"}
                </FieldDescription>
              </FieldContent>
              <Switch
                checked={repo.allowPublic}
                disabled={activation.updating}
                id="detail-allow-public"
                onCheckedChange={activation.setAllowPublic}
              />
            </Field>
          </DetailSection>
        ) : null}
      </SheetBody>
      {isAdmin ? (
        <SheetFooter>
          {repo.activated ? (
            <Button
              className="mr-auto text-danger-foreground hover:text-danger-foreground"
              onClick={() => setConfirming(true)}
              size="sm"
              variant="ghost"
            >
              Deactivate
            </Button>
          ) : null}
          <Link
            className={buttonVariants({ size: "sm", variant: "outline" })}
            href={migrateHref(org.login, repo)}
          >
            Move workflows
          </Link>
          {repo.activated ? null : (
            <Link
              className={buttonVariants({ size: "sm" })}
              href={activateHref(org.login, repo)}
            >
              Activate
            </Link>
          )}
        </SheetFooter>
      ) : null}
      <DeactivateDialog
        activation={activation}
        onOpenChange={setConfirming}
        open={confirming}
        repo={repo}
      />
    </>
  );
}

function ConfigDetail({ repo }: { repo: Repository }) {
  const { config } = repo;
  if (!config?.checkedAt) {
    return <p className="text-muted-foreground text-sm">Not read yet</p>;
  }
  return (
    <div className="grid gap-2 rounded-lg border p-3 text-sm">
      <div className="flex items-center justify-between gap-3">
        <span className="text-muted-foreground">
          Checked <RelativeTime value={config.checkedAt} />
        </span>
        {config.sha ? (
          <span className="rounded-sm bg-muted px-1.5 py-0.5 font-mono text-xs">
            {config.sha.slice(0, SHA_PREFIX)}
          </span>
        ) : null}
      </div>
      {config.error ? (
        <p className="text-danger-foreground">{config.error}</p>
      ) : null}
      {config.sha || config.error ? null : (
        <p className="text-muted-foreground">
          No airship.yaml on {repo.defaultBranch}; one-line labels still work
        </p>
      )}
    </div>
  );
}
