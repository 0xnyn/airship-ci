"use client";

import {
  Avatar,
  AvatarFallback,
  AvatarImage,
} from "@airship/ui/components/avatar";
import { Button } from "@airship/ui/components/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@airship/ui/components/dropdown-menu";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ChevronsUpDown,
  Cpu,
  LayoutDashboard,
  ListChecks,
  LogOut,
  Server,
} from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { type ReactNode, useEffect, useMemo } from "react";

import { $api, api } from "@/lib/api";
import { errorStatus } from "@/lib/api-errors";

import { ModeToggle } from "./mode-toggle";
import { NotFound } from "./not-found";
import { OrgContext, type OrgContextValue } from "./org-context";

const UNAUTHORIZED = 401;

const sections = [
  { href: "", icon: LayoutDashboard, label: "Overview" },
  { href: "/jobs", icon: ListChecks, label: "Jobs" },
  { href: "/runners", icon: Activity, label: "Runners" },
  { href: "/profiles", icon: Cpu, label: "Profiles" },
  { href: "/data-planes", icon: Server, label: "Data planes" },
] as const;

/** Sends signed-out visitors to sign in, then back to where they were. */
export function useSession() {
  const router = useRouter();
  const pathname = usePathname();
  const me = $api.useQuery("get", "/v1/me");
  const signedOut = errorStatus(me.error) === UNAUTHORIZED;

  useEffect(() => {
    if (signedOut) {
      router.replace(`/sign-in?next=${encodeURIComponent(pathname)}` as Route);
    }
  }, [signedOut, router, pathname]);

  return { ...me, signedOut };
}

export function Shell({
  children,
  org: login,
}: {
  children: ReactNode;
  org: string;
}) {
  const me = useSession();
  const org = me.data?.organizations?.find(
    (candidate) => candidate.login.toLowerCase() === login.toLowerCase()
  );
  const value = useMemo<OrgContextValue | null>(
    () => (org ? { isAdmin: org.role === "admin", org } : null),
    [org]
  );

  if (me.isPending || me.signedOut) {
    return <ShellSkeleton />;
  }
  if (me.isError || !me.data) {
    return (
      <NotFound
        description="The Airship API did not answer. Check that it is running and try again."
        title="Could not load your account"
      />
    );
  }
  if (!value) {
    return (
      <NotFound
        description={`You do not have access to ${login}, or the Airship GitHub App is not installed there.`}
        title="Organization not found"
      />
    );
  }

  return (
    <OrgContext value={value}>
      <div className="grid min-h-svh grid-cols-[minmax(0,1fr)] md:grid-cols-[14rem_minmax(0,1fr)]">
        <Sidebar me={me.data} org={value.org.login} />
        <main className="min-w-0">
          {value.org.suspended ? (
            <p className="border-b bg-amber-500/10 px-6 py-2 text-amber-700 text-xs dark:text-amber-400">
              The GitHub App installation for {value.org.login} is suspended.
              Airship will not start runners until it is unsuspended.
            </p>
          ) : null}
          <div className="mx-auto grid max-w-6xl gap-8 px-4 py-8 md:px-8">
            {children}
          </div>
        </main>
      </div>
    </OrgContext>
  );
}

type Me = NonNullable<ReturnType<typeof useSession>["data"]>;

function Sidebar({ me, org }: { me: Me; org: string }) {
  const pathname = usePathname();
  const base = `/${org}`;

  return (
    <aside className="flex min-w-0 flex-col gap-4 border-b bg-sidebar p-3 md:sticky md:top-0 md:h-svh md:border-r md:border-b-0">
      <div className="flex items-center justify-between gap-2 md:block">
        <Link
          className="hidden px-2 py-1 font-medium font-mono text-sm tracking-tight md:block"
          href={base as Route}
        >
          airship
        </Link>
        <div className="min-w-0 flex-1">
          <OrgSwitcher current={org} organizations={me.organizations ?? []} />
        </div>
        <div className="shrink-0 md:hidden">
          <UserMenu me={me} />
        </div>
      </div>
      <nav aria-label="Sections" className="-mx-1 overflow-x-auto md:mx-0">
        <ul className="flex gap-1 md:grid">
          {sections.map(({ href, icon: Icon, label }) => {
            const target = `${base}${href}`;
            const active =
              href === "" ? pathname === base : pathname.startsWith(target);
            return (
              <li key={label}>
                <Link
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "flex items-center gap-2 whitespace-nowrap px-2 py-1.5 text-muted-foreground text-xs hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                    active &&
                      "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
                  )}
                  href={target as Route}
                >
                  <Icon aria-hidden="true" className="size-3.5" />
                  {label}
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>
      <div className="mt-auto hidden items-center justify-between gap-2 md:flex">
        <UserMenu me={me} />
        <ModeToggle />
      </div>
    </aside>
  );
}

function OrgSwitcher({
  current,
  organizations,
}: {
  current: string;
  organizations: NonNullable<Me["organizations"]>;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            className="w-full shrink justify-between md:mt-3"
            variant="outline"
          />
        }
      >
        <span className="truncate">{current}</span>
        <ChevronsUpDown aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-48">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Organizations</DropdownMenuLabel>
          {organizations.map((org) => (
            <DropdownMenuItem
              key={org.installationId}
              render={<Link href={`/${org.login}` as Route} />}
            >
              <span className="truncate">{org.login}</span>
              <span className="ml-auto text-muted-foreground">{org.role}</span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function UserMenu({ me }: { me: Me }) {
  const router = useRouter();
  const queryClient = useQueryClient();

  const signOut = async () => {
    await api.POST("/v1/auth/sign-out");
    queryClient.clear();
    router.replace("/sign-in");
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            className="min-w-0 justify-start gap-2 px-1.5"
            variant="ghost"
          />
        }
      >
        <Avatar className="size-5">
          {me.avatarUrl ? <AvatarImage alt="" src={me.avatarUrl} /> : null}
          <AvatarFallback>{me.login.slice(0, 1).toUpperCase()}</AvatarFallback>
        </Avatar>
        <span className="truncate">{me.login}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-44">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{me.name ?? me.login}</DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={signOut}>
          <LogOut aria-hidden="true" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ShellSkeleton() {
  return (
    <div className="grid min-h-svh grid-cols-[minmax(0,1fr)] md:grid-cols-[14rem_minmax(0,1fr)]">
      <div className="hidden border-r bg-sidebar p-3 md:grid md:content-start md:gap-2">
        <Skeleton className="h-6 w-20" />
        <Skeleton className="mt-3 h-8" />
        <Skeleton className="h-6" />
        <Skeleton className="h-6" />
        <Skeleton className="h-6" />
      </div>
      <div className="mx-auto grid w-full max-w-6xl content-start gap-6 px-4 py-8 md:px-8">
        <Skeleton className="h-7 w-40" />
        <Skeleton className="h-32" />
      </div>
    </div>
  );
}
