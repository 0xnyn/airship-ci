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
import { Kbd, KbdGroup } from "@airship/ui/components/kbd";
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
  Plus,
  Search,
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

import { CommandMenu } from "./command-menu";
import { Logo } from "./logo";
import { NotFound } from "./not-found";
import { OrgContext, type OrgContextValue } from "./org-context";
import { OrgMark } from "./org-mark";
import {
  currentSection,
  type Section,
  sectionGroups,
  sections,
} from "./sections";

const UNAUTHORIZED = 401;
const POLL_MS = 10_000;
const DOCS_URL = "https://docs.airshipci.com";
const COLLAPSED_KEY = "airship.sidebar.collapsed";
/** How long after G the second key of a shortcut still counts. */
const SEQUENCE_MS = 1200;

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
 * Whether the desktop sidebar is collapsed to its rail of icons, remembered
 * per browser, with ⌘B / Ctrl+B to toggle it from anywhere.
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

/** Whether a key press belongs to a text field rather than to the page. */
function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  return (
    target.isContentEditable ||
    ["INPUT", "SELECT", "TEXTAREA"].includes(target.tagName)
  );
}

/**
 * The keyboard: ⌘K / Ctrl+K opens the command menu, and G followed by a
 * section's key (G J for jobs) goes straight there.
 */
function useShortcuts(base: string, openCommands: () => void) {
  const router = useRouter();

  useEffect(() => {
    let pendingSince = 0;
    const onKey = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      if (key === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        openCommands();
        return;
      }
      if (
        event.metaKey ||
        event.ctrlKey ||
        event.altKey ||
        isTyping(event.target)
      ) {
        return;
      }
      if (pendingSince && Date.now() - pendingSince < SEQUENCE_MS) {
        pendingSince = 0;
        const section = sections.find((s) => s.shortcut === key);
        if (section) {
          event.preventDefault();
          router.push(`${base}${section.href}` as Route);
        }
        return;
      }
      pendingSince = key === "g" ? Date.now() : 0;
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [base, openCommands, router]);
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
  const [commandsOpen, setCommandsOpen] = useState(false);
  const pathname = usePathname();
  const base = `/${org?.login ?? login}`;
  const openCommands = useMemo(() => () => setCommandsOpen(true), []);
  useShortcuts(base, openCommands);

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
          onSearch={openCommands}
          org={value.org.login}
        />
        <div className="flex min-w-0 flex-1 flex-col md:py-(--shell-inset) md:pr-(--shell-inset)">
          <main className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden bg-background md:rounded-xl md:border md:shadow-inset">
            <TopBar
              collapsed={collapsed}
              onOpenDrawer={() => setDrawerOpen(true)}
              onToggle={() => setCollapsed((was) => !was)}
              org={value.org.login}
              role={value.org.role}
            />
            <div className="min-h-0 flex-1 overflow-y-auto">
              {value.org.suspended ? (
                <p className="flex items-center gap-2 border-b bg-warning-surface px-6 py-2.5 text-warning-foreground text-xs">
                  <TriangleAlert aria-hidden="true" className="size-3.5" />
                  Airship is not starting runners: the GitHub App installation
                  for {value.org.login} is suspended.
                </p>
              ) : null}
              <div className="mx-auto grid w-full max-w-(--content-width) gap-8 px-4 pt-8 pb-16 md:px-10 md:pt-10">
                {children}
              </div>
            </div>
          </main>
        </div>
      </div>
      <CommandMenu
        onOpenChange={setCommandsOpen}
        open={commandsOpen}
        org={value.org.login}
        organizations={me.data.organizations ?? []}
      />
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

/** Hidden on the desktop rail, where only icons fit. */
const RAIL_HIDDEN = "md:group-data-[state=collapsed]/sidebar:hidden";

function Sidebar({
  collapsed,
  drawerOpen,
  me,
  onSearch,
  org,
}: {
  collapsed: boolean;
  drawerOpen: boolean;
  me: Me;
  onSearch: () => void;
  org: string;
}) {
  const pathname = usePathname();
  const base = `/${org}`;
  const counts = useLiveCounts(org);
  const desktop = useDesktop();
  // Off screen is out of the tab order too.
  const hidden = desktop ? false : !drawerOpen;
  const rail = desktop && collapsed;

  return (
    <aside
      aria-label="Navigation"
      className={cn(
        "group/sidebar",
        // Phones: a drawer over the page.
        "fixed inset-y-0 left-0 z-50 flex w-72 flex-col bg-sidebar transition-transform duration-300 ease-smooth",
        drawerOpen ? "translate-x-0" : "-translate-x-full",
        // Desktop: a column beside the inset panel, collapsible to a rail.
        "md:static md:z-auto md:translate-x-0 md:overflow-hidden md:transition-[width] md:duration-300",
        collapsed ? "md:w-(--sidebar-rail-width)" : "md:w-(--sidebar-width)"
      )}
      data-state={rail ? "collapsed" : "expanded"}
      inert={hidden}
    >
      <div className="flex h-full w-72 min-w-0 flex-col gap-2 px-2 py-2 md:w-full">
        <OrgSwitcher current={org} me={me} />

        <button
          aria-keyshortcuts="Meta+K Control+K"
          className="flex h-8 w-full items-center gap-2 rounded-md border bg-background px-2 text-muted-foreground text-sm outline-none transition-colors hover:border-input hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring md:group-data-[state=collapsed]/sidebar:justify-center md:group-data-[state=collapsed]/sidebar:border-transparent md:group-data-[state=collapsed]/sidebar:bg-transparent"
          onClick={onSearch}
          title="Find (⌘K)"
          type="button"
        >
          <Search aria-hidden="true" className="size-4 shrink-0" />
          <span className={cn("flex-1 text-left", RAIL_HIDDEN)}>Find…</span>
          <KbdGroup className={RAIL_HIDDEN}>
            <Kbd className="bg-sidebar">⌘</Kbd>
            <Kbd className="bg-sidebar">K</Kbd>
          </KbdGroup>
        </button>

        <nav
          aria-label="Sections"
          className="-mx-2 mt-2 grid min-h-0 flex-1 content-start gap-4 overflow-y-auto overflow-x-hidden px-2"
        >
          {sectionGroups.map((group) => (
            <div className="grid gap-0.5" key={group.label}>
              <p
                className={cn(
                  "truncate px-2 pb-1 font-medium text-muted-foreground text-xs",
                  RAIL_HIDDEN
                )}
              >
                {group.label}
              </p>
              <ul className="grid gap-0.5">
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

        <div className="border-sidebar-border border-t pt-2">
          <UserMenu me={me} />
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
  const { href, icon: Icon, label, shortcut } = section;
  return (
    <Link
      aria-current={active ? "page" : undefined}
      aria-keyshortcuts={`g ${shortcut}`}
      className={cn(
        "group/item relative flex h-8 items-center gap-2.5 rounded-md px-2 text-sidebar-foreground text-sm transition-colors duration-150",
        "hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
        "md:group-data-[state=collapsed]/sidebar:justify-center",
        active && "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
      )}
      href={`${base}${href}` as Route}
      title={label}
    >
      <span className="relative shrink-0">
        <Icon aria-hidden="true" className="size-4" />
        {count ? (
          <span
            aria-hidden="true"
            className="absolute -top-0.5 -right-0.5 hidden size-1.5 rounded-full bg-active ring-2 ring-sidebar md:group-data-[state=collapsed]/sidebar:block"
          />
        ) : null}
      </span>
      <span className={cn("truncate", RAIL_HIDDEN)}>{label}</span>
      <span
        className={cn(
          "ml-auto flex items-center gap-1.5 text-muted-foreground",
          RAIL_HIDDEN
        )}
      >
        <KbdGroup className="hidden group-hover/item:inline-flex">
          <Kbd className="bg-transparent">G</Kbd>
          <Kbd className="bg-transparent">{shortcut.toUpperCase()}</Kbd>
        </KbdGroup>
        {count ? (
          <span className="figure flex h-5 min-w-5 items-center justify-center gap-1 rounded-full bg-active-surface px-1.5 font-medium text-[11px] text-active-foreground group-hover/item:hidden">
            {count}
            <span className="sr-only"> active</span>
          </span>
        ) : null}
      </span>
    </Link>
  );
}

const themes = [
  { icon: Sun, label: "Light", value: "light" },
  { icon: Moon, label: "Dark", value: "dark" },
  { icon: Monitor, label: "System", value: "system" },
] as const;

/** The organization you are in, and every other one you can switch to. */
function OrgSwitcher({ current, me }: { current: string; me: Me }) {
  const organizations = me.organizations ?? [];
  const active = organizations.find(
    (org) => org.login.toLowerCase() === current.toLowerCase()
  );

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            aria-label={`${current}. Switch organization`}
            className="flex h-10 w-full min-w-0 items-center gap-2 rounded-md px-2 text-left outline-none transition-colors hover:bg-sidebar-accent focus-visible:ring-1 focus-visible:ring-ring aria-expanded:bg-sidebar-accent md:group-data-[state=collapsed]/sidebar:justify-center md:group-data-[state=collapsed]/sidebar:px-0"
            type="button"
          />
        }
      >
        <OrgMark className="size-6" login={current} />
        <span className={cn("grid min-w-0 flex-1 leading-tight", RAIL_HIDDEN)}>
          <span className="truncate font-medium text-sm">{current}</span>
        </span>
        <span
          className={cn(
            "shrink-0 rounded-full border px-1.5 py-px text-[10px] text-muted-foreground capitalize",
            RAIL_HIDDEN
          )}
        >
          {active?.role ?? "member"}
        </span>
        <ChevronsUpDown
          aria-hidden="true"
          className={cn("size-3.5 shrink-0 text-muted-foreground", RAIL_HIDDEN)}
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
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
                <span className="ml-auto text-muted-foreground text-xs capitalize">
                  {org.role}
                </span>
              )}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          render={
            <a
              href={`${DOCS_URL}/getting-started`}
              rel="noopener"
              target="_blank"
            />
          }
        >
          <Plus aria-hidden="true" />
          Add an organization
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** Who you are, the theme, and signing out. */
function UserMenu({ me }: { me: Me }) {
  const router = useRouter();
  const { setTheme, theme } = useTheme();
  const queryClient = useQueryClient();

  const signOut = async () => {
    const { error } = await api.POST("/v1/auth/sign-out");
    if (error) {
      toast.error(errorMessage(error, "Could not sign out"));
      return;
    }
    queryClient.clear();
    router.replace("/sign-in");
  };

  const initial = me.login.slice(0, 1).toUpperCase();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            aria-label={`Signed in as ${me.login}. Open account menu`}
            className="flex h-10 w-full min-w-0 items-center gap-2 rounded-md px-2 text-left outline-none transition-colors hover:bg-sidebar-accent focus-visible:ring-1 focus-visible:ring-ring aria-expanded:bg-sidebar-accent md:group-data-[state=collapsed]/sidebar:justify-center md:group-data-[state=collapsed]/sidebar:px-0"
            type="button"
          />
        }
      >
        <Avatar className="size-6 shrink-0 rounded-full">
          {me.avatarUrl ? <AvatarImage alt="" src={me.avatarUrl} /> : null}
          <AvatarFallback className="text-[10px]">{initial}</AvatarFallback>
        </Avatar>
        <span className={cn("grid min-w-0 flex-1 leading-tight", RAIL_HIDDEN)}>
          <span className="truncate font-medium text-[13px]">
            {me.name ?? me.login}
          </span>
          <span className="truncate text-muted-foreground text-xs">
            @{me.login}
          </span>
        </span>
        <ChevronsUpDown
          aria-hidden="true"
          className={cn("size-3.5 shrink-0 text-muted-foreground", RAIL_HIDDEN)}
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-60" side="top">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="grid leading-tight">
            <span className="truncate font-medium text-foreground text-sm">
              {me.name ?? me.login}
            </span>
            <span className="truncate">@{me.login}</span>
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <div className="flex items-center justify-between px-2 py-1.5">
            <span className="font-medium text-muted-foreground text-xs">
              Theme
            </span>
            <fieldset
              aria-label="Theme"
              className="flex gap-0.5 rounded-md border p-0.5"
            >
              {themes.map(({ icon: Icon, label, value }) => (
                <label
                  className={cn(
                    "grid size-7 cursor-pointer place-items-center rounded-[5px] text-muted-foreground transition-colors hover:text-foreground has-[:focus-visible]:ring-1 has-[:focus-visible]:ring-ring",
                    theme === value && "bg-muted text-foreground shadow-sm"
                  )}
                  key={value}
                  title={label}
                >
                  <input
                    checked={theme === value}
                    className="sr-only"
                    name="theme"
                    onChange={() => setTheme(value)}
                    type="radio"
                    value={value}
                  />
                  <Icon aria-hidden="true" className="size-3.5" />
                </label>
              ))}
            </fieldset>
          </div>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onClick={() => window.open(DOCS_URL, "_blank", "noopener")}
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

/** A breadcrumb separator, drawn as Vercel-style slanted hairline. */
function Slash() {
  return (
    <svg
      aria-hidden="true"
      className="size-4 shrink-0 text-faint"
      fill="none"
      viewBox="0 0 16 16"
    >
      <path
        d="M10.5 2.5 5.5 13.5"
        stroke="currentColor"
        strokeLinecap="round"
        strokeWidth="1.25"
      />
    </svg>
  );
}

/** The inset panel's header: where you are, and whether it is live. */
function TopBar({
  collapsed,
  onOpenDrawer,
  onToggle,
  org,
  role,
}: {
  collapsed: boolean;
  onOpenDrawer: () => void;
  onToggle: () => void;
  org: string;
  role: string;
}) {
  const pathname = usePathname();
  const current = currentSection(pathname, `/${org}`);

  return (
    <header className="flex h-(--topbar-height) shrink-0 items-center gap-1.5 border-b px-3 md:px-4">
      <Button
        aria-label="Open navigation"
        className="text-muted-foreground md:hidden"
        onClick={onOpenDrawer}
        size="icon-sm"
        variant="ghost"
      >
        <Menu />
      </Button>
      <Button
        aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        className="hidden text-muted-foreground md:inline-flex"
        onClick={onToggle}
        size="icon-sm"
        title={collapsed ? "Expand sidebar (⌘B)" : "Collapse sidebar (⌘B)"}
        variant="ghost"
      >
        <PanelLeft />
      </Button>
      <span
        aria-hidden="true"
        className="mx-1 hidden h-4 w-px bg-border md:block"
      />
      <nav
        aria-label="Breadcrumb"
        className="flex min-w-0 items-center gap-1 text-sm"
      >
        <Link
          aria-label="Airship"
          className="hidden shrink-0 text-foreground sm:inline-flex"
          href={`/${org}` as Route}
        >
          <Logo size={16} wordmark={false} />
        </Link>
        <span className="hidden sm:contents">
          <Slash />
        </span>
        <Link
          className="flex min-w-0 items-center gap-2 rounded-md px-1.5 py-1 font-medium transition-colors hover:bg-accent"
          href={`/${org}` as Route}
        >
          <OrgMark className="size-4 text-[9px]" login={org} />
          <span className="truncate">{org}</span>
          <span className="hidden rounded-full border px-1.5 py-px font-normal text-[10px] text-muted-foreground capitalize sm:inline">
            {role}
          </span>
        </Link>
        {current ? (
          <>
            <Slash />
            <span
              aria-current="page"
              className="truncate px-1.5 text-muted-foreground"
            >
              {current.section.label}
            </span>
          </>
        ) : null}
      </nav>
      <div className="ml-auto flex items-center gap-2">
        <span
          className="hidden h-7 items-center gap-1.5 rounded-full border px-2.5 text-muted-foreground text-xs sm:flex"
          title="This page refreshes itself"
        >
          <span aria-hidden="true" className="relative flex size-1.5">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-60" />
            <span className="relative inline-flex size-1.5 rounded-full bg-success" />
          </span>
          Live
        </span>
        <Button
          className="hidden text-muted-foreground sm:inline-flex"
          nativeButton={false}
          render={<a href={DOCS_URL} rel="noopener" target="_blank" />}
          size="sm"
          variant="ghost"
        >
          Docs
        </Button>
      </div>
    </header>
  );
}

function ShellSkeleton() {
  return (
    <div className="flex h-svh bg-sidebar">
      <div className="hidden w-(--sidebar-width) shrink-0 flex-col gap-3 p-2 md:flex">
        <Skeleton className="h-10" />
        <Skeleton className="h-8" />
        <div className="mt-3 grid gap-1.5">
          <Skeleton className="h-3 w-12" />
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
          <Skeleton className="h-7" />
        </div>
        <Skeleton className="mt-auto h-10" />
      </div>
      <div className="flex min-w-0 flex-1 md:py-(--shell-inset) md:pr-(--shell-inset)">
        <div className="flex-1 bg-background md:rounded-xl md:border">
          <div className="h-(--topbar-height) border-b" />
          <div className="mx-auto grid w-full max-w-(--content-width) content-start gap-6 px-4 py-10 md:px-10">
            <Skeleton className="h-8 w-48" />
            <Skeleton className="h-4 w-72" />
            <Skeleton className="h-28" />
          </div>
        </div>
      </div>
    </div>
  );
}
