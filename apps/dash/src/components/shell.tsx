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
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@airship/ui/components/dropdown-menu";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import { useQueryClient } from "@tanstack/react-query";
import {
  BookOpen,
  Check,
  ChevronsUpDown,
  LogOut,
  Menu,
  Monitor,
  Moon,
  PanelLeft,
  Sun,
  TriangleAlert,
} from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useTheme } from "next-themes";
import {
  type ReactNode,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import { toast } from "sonner";

import { $api, api } from "@/lib/api";
import { errorMessage, errorStatus } from "@/lib/api-errors";
import { isActiveSection, nextFor } from "@/lib/navigation";

import { Logo } from "./logo";
import { NotFound } from "./not-found";
import { OrgContext, type OrgContextValue } from "./org-context";
import {
  accentDot,
  currentSection,
  type Section,
  sectionGroups,
} from "./sections";

const UNAUTHORIZED = 401;
const POLL_MS = 10_000;
const DOCS_URL = "https://docs.airshipci.com";
const COLLAPSED_KEY = "airship.sidebar.collapsed";

/** Sends signed-out visitors to sign in, then back to where they were. */
export function useSession() {
  const router = useRouter();
  const pathname = usePathname();
  const me = $api.useQuery("get", "/v1/me");
  const signedOut = errorStatus(me.error) === UNAUTHORIZED;

  useEffect(() => {
    if (signedOut) {
      const next = nextFor(pathname, window.location.search);
      router.replace(`/sign-in?next=${encodeURIComponent(next)}` as Route);
    }
  }, [signedOut, router, pathname]);

  return { ...me, signedOut };
}

const DESKTOP = "(min-width: 768px)";

/** Whether the viewport is wide enough for the sidebar to sit beside the page. */
function useDesktop(): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const query = window.matchMedia(DESKTOP);
      query.addEventListener("change", onChange);
      return () => query.removeEventListener("change", onChange);
    },
    () => window.matchMedia(DESKTOP).matches,
    () => true
  );
}

/**
 * Whether the desktop sidebar is collapsed, remembered per browser, with
 * ⌘B / Ctrl+B to toggle it from anywhere.
 */
function useCollapsed() {
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    try {
      setCollapsed(window.localStorage.getItem(COLLAPSED_KEY) === "1");
    } catch {
      // Storage can be unavailable (private windows); stay expanded.
    }
  }, []);

  useEffect(() => {
    try {
      window.localStorage.setItem(COLLAPSED_KEY, collapsed ? "1" : "0");
    } catch {
      // Not remembering is fine.
    }
  }, [collapsed]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() === "b" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        setCollapsed((value) => !value);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return [collapsed, setCollapsed] as const;
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
  const [collapsed, setCollapsed] = useCollapsed();
  const [drawerOpen, setDrawerOpen] = useState(false);
  const pathname = usePathname();

  // A navigation closes the phone drawer.
  // biome-ignore lint/correctness/useExhaustiveDependencies: runs per path.
  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname]);

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
      <div className="flex h-svh overflow-hidden bg-sidebar">
        {drawerOpen ? (
          <button
            aria-label="Close navigation"
            className="fixed inset-0 z-40 bg-black/50 backdrop-blur-[2px] md:hidden"
            onClick={() => setDrawerOpen(false)}
            type="button"
          />
        ) : null}
        <Sidebar
          collapsed={collapsed}
          drawerOpen={drawerOpen}
          me={me.data}
          onCollapse={() => setCollapsed(true)}
          org={value.org.login}
        />
        <div
          className={cn(
            "flex min-w-0 flex-1 flex-col md:py-(--shell-inset) md:pr-(--shell-inset)",
            collapsed && "md:pl-(--shell-inset)"
          )}
        >
          <main className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background md:rounded-xl md:border md:shadow-inset">
            <TopBar
              collapsed={collapsed}
              onExpand={() => setCollapsed(false)}
              onOpenDrawer={() => setDrawerOpen(true)}
              org={value.org.login}
            />
            <div className="min-h-0 flex-1 overflow-y-auto">
              {value.org.suspended ? (
                <p className="flex items-center gap-2 border-b bg-warning-surface px-6 py-2.5 text-warning-foreground text-xs">
                  <TriangleAlert aria-hidden="true" className="size-3.5" />
                  The GitHub App installation for {value.org.login} is
                  suspended. Airship will not start runners until it is
                  unsuspended.
                </p>
              ) : null}
              <div className="mx-auto grid w-full max-w-(--content-width) gap-8 px-4 pt-8 pb-16 md:px-8 md:pt-10">
                {children}
              </div>
            </div>
          </main>
        </div>
      </div>
    </OrgContext>
  );
}

type Me = NonNullable<ReturnType<typeof useSession>["data"]>;

/** Live counts beside a section's name: what is running right now. */
function useLiveCounts(org: string): Record<string, number> {
  const overview = $api.useQuery(
    "get",
    "/v1/orgs/{org}/overview",
    { params: { path: { org } } },
    { refetchInterval: POLL_MS }
  );
  const { data } = overview;
  if (!data) {
    return {};
  }
  const { runners } = data;
  return {
    "/jobs": data.queued + data.inProgress,
    "/runners":
      runners.pending + runners.starting + runners.idle + runners.busy,
  };
}

function Sidebar({
  collapsed,
  drawerOpen,
  me,
  onCollapse,
  org,
}: {
  collapsed: boolean;
  drawerOpen: boolean;
  me: Me;
  onCollapse: () => void;
  org: string;
}) {
  const pathname = usePathname();
  const base = `/${org}`;
  const counts = useLiveCounts(org);
  const desktop = useDesktop();
  // Off screen is out of the tab order too.
  const hidden = desktop ? collapsed : !drawerOpen;

  return (
    <aside
      aria-label="Navigation"
      className={cn(
        // Phones: a drawer over the page.
        "fixed inset-y-0 left-0 z-50 flex w-72 flex-col bg-sidebar transition-transform duration-300 ease-smooth",
        drawerOpen ? "translate-x-0" : "-translate-x-full",
        // Desktop: a column beside the inset panel, collapsible to nothing.
        "md:static md:z-auto md:translate-x-0 md:overflow-hidden md:transition-[width] md:duration-300",
        collapsed ? "md:w-0" : "md:w-(--sidebar-width)"
      )}
      data-state={collapsed ? "collapsed" : "expanded"}
      inert={hidden}
    >
      <div className="flex h-full w-72 min-w-0 flex-col gap-5 px-3 pt-3 pb-3 md:w-(--sidebar-width)">
        <div className="flex h-9 items-center justify-between pl-2">
          <Link aria-label="Airship home" href={base as Route}>
            <Logo />
          </Link>
          <Button
            aria-label="Collapse sidebar"
            className="hidden text-muted-foreground md:inline-flex"
            onClick={onCollapse}
            size="icon-sm"
            title="Collapse sidebar (⌘B)"
            variant="ghost"
          >
            <PanelLeft />
          </Button>
        </div>

        <nav
          aria-label="Sections"
          className="-mr-1 grid min-h-0 flex-1 content-start gap-5 overflow-y-auto pr-1"
        >
          {sectionGroups.map((group) => (
            <div className="grid gap-px" key={group.label}>
              <p className="px-2 pb-1.5 font-mono text-[11px] text-muted-foreground tracking-label">
                {group.label}
              </p>
              <ul className="grid gap-px">
                {group.sections.map((section) => (
                  <li key={section.label}>
                    <NavItem
                      active={isActiveSection(pathname, base, section.href)}
                      base={base}
                      count={counts[section.href]}
                      section={section}
                    />
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>

        <div className="border-sidebar-border border-t pt-3">
          <AccountMenu current={org} me={me} />
        </div>
      </div>
    </aside>
  );
}

function NavItem({
  active,
  base,
  count,
  section,
}: {
  active: boolean;
  base: string;
  count: number | undefined;
  section: Section;
}) {
  const { href, icon: Icon, label, accent } = section;
  return (
    <Link
      aria-current={active ? "page" : undefined}
      className={cn(
        "group relative flex h-8 items-center gap-2.5 rounded-md px-2 text-[13px] text-sidebar-foreground transition-colors duration-150",
        "hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
        active && "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
      )}
      href={`${base}${href}` as Route}
    >
      {/* The landing page marks its open feature with a short bar; the
          current section gets the same mark, in the section's colour. */}
      <span
        aria-hidden="true"
        className={cn(
          "absolute top-1/2 -left-3 h-4 w-[3px] -translate-y-1/2 rounded-r-full transition-opacity duration-200",
          accentDot[accent],
          active ? "opacity-100" : "opacity-0"
        )}
      />
      <Icon
        aria-hidden="true"
        className={cn(
          "size-4 shrink-0 transition-opacity",
          active ? "opacity-100" : "opacity-60 group-hover:opacity-90"
        )}
      />
      <span className="truncate">{label}</span>
      {count ? (
        <span className="ml-auto flex items-center gap-1.5 font-mono text-[11px] text-muted-foreground tabular-nums">
          <span
            aria-hidden="true"
            className={cn(
              "size-1.5 animate-signal rounded-full",
              accentDot[accent]
            )}
          />
          {count}
          <span className="sr-only"> active</span>
        </span>
      ) : null}
    </Link>
  );
}

function OrgMark({ login, className }: { className?: string; login: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid size-6 shrink-0 place-items-center rounded-md bg-linear-to-br from-active to-info font-medium font-mono text-[11px] text-white uppercase",
        className
      )}
    >
      {login.slice(0, 1)}
    </span>
  );
}

/**
 * The one control at the foot of the sidebar: which organization you are
 * in and who you are, with switching organizations, the docs and signing
 * out as options inside it.
 */
const themes = [
  { icon: Sun, label: "Light", value: "light" },
  { icon: Moon, label: "Dark", value: "dark" },
  { icon: Monitor, label: "System", value: "system" },
] as const;

function AccountMenu({ current, me }: { current: string; me: Me }) {
  const router = useRouter();
  const { setTheme, theme } = useTheme();
  const queryClient = useQueryClient();
  const organizations = me.organizations ?? [];
  const active = organizations.find(
    (org) => org.login.toLowerCase() === current.toLowerCase()
  );

  const signOut = async () => {
    const { error } = await api.POST("/v1/auth/sign-out");
    if (error) {
      toast.error(errorMessage(error, "Could not sign out"));
      return;
    }
    queryClient.clear();
    router.replace("/sign-in");
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            aria-label={`${current}, signed in as ${me.login}. Open account menu`}
            className="flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg border border-sidebar-border bg-card/60 px-2 text-left outline-none transition-colors hover:bg-card focus-visible:ring-1 focus-visible:ring-ring aria-expanded:bg-card"
            type="button"
          />
        }
      >
        <span className="relative shrink-0">
          <OrgMark className="size-7 text-xs" login={current} />
          <Avatar className="absolute -right-1 -bottom-1 size-4 rounded-full ring-2 ring-sidebar">
            {me.avatarUrl ? <AvatarImage alt="" src={me.avatarUrl} /> : null}
            <AvatarFallback className="text-[8px]">
              {me.login.slice(0, 1).toUpperCase()}
            </AvatarFallback>
          </Avatar>
        </span>
        <span className="grid min-w-0 flex-1 leading-tight">
          <span className="truncate font-medium text-[13px]">{current}</span>
          <span className="truncate font-mono text-[11px] text-muted-foreground">
            @{me.login} · {active?.role ?? "member"}
          </span>
        </span>
        <ChevronsUpDown
          aria-hidden="true"
          className="size-3.5 shrink-0 text-muted-foreground"
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        className="w-(--anchor-width) min-w-60"
        side="top"
      >
        <DropdownMenuGroup>
          <DropdownMenuLabel className="flex items-center gap-2.5 py-2">
            <Avatar className="size-7 rounded-full">
              {me.avatarUrl ? <AvatarImage alt="" src={me.avatarUrl} /> : null}
              <AvatarFallback>
                {me.login.slice(0, 1).toUpperCase()}
              </AvatarFallback>
            </Avatar>
            <span className="grid min-w-0 leading-tight">
              <span className="truncate font-medium text-foreground text-xs">
                {me.name ?? me.login}
              </span>
              <span className="truncate font-mono text-[11px]">
                @{me.login}
              </span>
            </span>
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuLabel>Organizations</DropdownMenuLabel>
          {organizations.map((org) => (
            <DropdownMenuItem
              key={org.installationId}
              render={<Link href={`/${org.login}` as Route} />}
            >
              <OrgMark className="size-5 text-[10px]" login={org.login} />
              <span className="truncate">{org.login}</span>
              {org.login.toLowerCase() === current.toLowerCase() ? (
                <Check aria-label="Current" className="ml-auto" />
              ) : (
                <span className="ml-auto font-mono text-[11px] text-muted-foreground">
                  {org.role}
                </span>
              )}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuLabel>Theme</DropdownMenuLabel>
          <DropdownMenuRadioGroup onValueChange={setTheme} value={theme}>
            {themes.map(({ icon: Icon, label, value }) => (
              <DropdownMenuRadioItem key={value} value={value}>
                <Icon aria-hidden="true" />
                {label}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          render={<a href={DOCS_URL} rel="noopener" target="_blank" />}
        >
          <BookOpen aria-hidden="true" />
          Documentation
        </DropdownMenuItem>
        <DropdownMenuItem onClick={signOut}>
          <LogOut aria-hidden="true" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** The inset panel's header: where you are, and whether it is live. */
function TopBar({
  collapsed,
  onExpand,
  onOpenDrawer,
  org,
}: {
  collapsed: boolean;
  onExpand: () => void;
  onOpenDrawer: () => void;
  org: string;
}) {
  const pathname = usePathname();
  const current = currentSection(pathname, `/${org}`);

  return (
    <header className="flex h-(--topbar-height) shrink-0 items-center gap-2 border-b px-3 md:px-4">
      <Button
        aria-label="Open navigation"
        className="md:hidden"
        onClick={onOpenDrawer}
        size="icon-sm"
        variant="ghost"
      >
        <Menu />
      </Button>
      {collapsed ? (
        <Button
          aria-label="Expand sidebar"
          className="hidden md:inline-flex"
          onClick={onExpand}
          size="icon-sm"
          title="Expand sidebar (⌘B)"
          variant="ghost"
        >
          <PanelLeft />
        </Button>
      ) : null}
      <nav
        aria-label="Breadcrumb"
        className="flex min-w-0 items-center gap-2 font-mono text-muted-foreground text-xs tracking-label"
      >
        <Logo className="text-muted-foreground" size={12} wordmark={false} />
        <Link
          className="truncate transition-colors hover:text-foreground"
          href={`/${org}` as Route}
        >
          {org}
        </Link>
        {current ? (
          <>
            <span aria-hidden="true" className="text-faint">
              /
            </span>
            <span aria-current="page" className="truncate text-foreground">
              {current.section.label}
            </span>
          </>
        ) : null}
      </nav>
      <div className="ml-auto flex items-center gap-3">
        <span
          className="hidden items-center gap-1.5 font-mono text-[11px] text-muted-foreground sm:flex"
          title="This page refreshes itself"
        >
          <span
            aria-hidden="true"
            className="size-1.5 animate-signal rounded-full bg-success"
          />
          live
        </span>
      </div>
    </header>
  );
}

function ShellSkeleton() {
  return (
    <div className="flex h-svh bg-sidebar">
      <div className="hidden w-(--sidebar-width) shrink-0 flex-col gap-5 p-3 md:flex">
        <Skeleton className="h-9 w-24" />
        <div className="grid gap-1.5">
          <Skeleton className="h-3 w-12" />
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
        </div>
        <Skeleton className="mt-auto h-12" />
      </div>
      <div className="flex min-w-0 flex-1 md:py-(--shell-inset) md:pr-(--shell-inset)">
        <div className="flex-1 bg-background md:rounded-xl md:border">
          <div className="h-(--topbar-height) border-b" />
          <div className="mx-auto grid w-full max-w-(--content-width) content-start gap-6 px-4 py-10 md:px-8">
            <Skeleton className="h-3 w-16" />
            <Skeleton className="h-7 w-48" />
            <Skeleton className="h-28" />
          </div>
        </div>
      </div>
    </div>
  );
}
