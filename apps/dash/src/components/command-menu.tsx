"use client";

import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@airship/ui/components/dialog";
import { Kbd, KbdGroup } from "@airship/ui/components/kbd";
import { cn } from "@airship/ui/lib/utils";
import {
  BookOpen,
  CloudUpload,
  Compass,
  CornerDownLeft,
  FolderPlus,
  GitPullRequest,
  type LucideIcon,
  Monitor,
  Moon,
  Search,
  Sun,
} from "lucide-react";
import type { Route } from "next";
import { useRouter } from "next/navigation";
import { useTheme } from "next-themes";
import { type KeyboardEvent, useId, useMemo, useState } from "react";

import { OrgMark } from "./org-mark";
import { sections } from "./sections";

const DOCS_URL = "https://docs.airshipci.com";

interface Command {
  group: string;
  icon?: LucideIcon;
  id: string;
  /** Matched against the query along with the label. */
  keywords?: string;
  label: string;
  mark?: string;
  run: () => void;
  shortcut?: string[];
}

/**
 * The ⌘K palette: jump to a section, switch organization or theme, or open
 * the docs, all from the keyboard.
 */
export function CommandMenu({
  onOpenChange,
  open,
  org,
  organizations,
}: {
  onOpenChange: (open: boolean) => void;
  open: boolean;
  org: string;
  organizations: readonly { login: string }[];
}) {
  const router = useRouter();
  const { setTheme } = useTheme();
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const listId = useId();

  const commands = useMemo<Command[]>(() => {
    const close = (run: () => void) => () => {
      onOpenChange(false);
      run();
    };
    return [
      ...sections.map((section) => ({
        group: "Go to",
        icon: section.icon,
        id: `go:${section.href}`,
        label: section.label,
        run: close(() => router.push(`/${org}${section.href}` as Route)),
        shortcut: ["G", section.shortcut.toUpperCase()],
      })),
      ...(
        [
          ["/repositories/new", "Add repository", FolderPlus, "activate repo"],
          ["/cloud/connect", "Connect cloud", CloudUpload, "aws gcp azure"],
          [
            "/repositories/migrate",
            "Move workflows",
            GitPullRequest,
            "migrate runs-on",
          ],
          ["/setup", "Guided setup", Compass, "onboarding start"],
        ] as const
      ).map(([href, label, icon, keywords]) => ({
        group: "Actions",
        icon,
        id: `do:${href}`,
        keywords,
        label,
        run: close(() => router.push(`/${org}${href}` as Route)),
      })),
      ...organizations
        .filter((candidate) => candidate.login !== org)
        .map((candidate) => ({
          group: "Organizations",
          id: `org:${candidate.login}`,
          keywords: "switch organization team",
          label: `Switch to ${candidate.login}`,
          mark: candidate.login,
          run: close(() => router.push(`/${candidate.login}` as Route)),
        })),
      ...(
        [
          ["light", "Light", Sun],
          ["dark", "Dark", Moon],
          ["system", "System", Monitor],
        ] as const
      ).map(([value, label, icon]) => ({
        group: "Theme",
        icon,
        id: `theme:${value}`,
        keywords: "theme appearance mode",
        label: `${label} theme`,
        run: close(() => setTheme(value)),
      })),
      {
        group: "Help",
        icon: BookOpen,
        id: "docs",
        keywords: "help guide reference",
        label: "Documentation",
        run: close(() => window.open(DOCS_URL, "_blank", "noopener")),
      },
    ];
  }, [onOpenChange, org, organizations, router, setTheme]);

  const needle = query.trim().toLowerCase();
  const matches = needle
    ? commands.filter((command) =>
        `${command.label} ${command.keywords ?? ""}`
          .toLowerCase()
          .includes(needle)
      )
    : commands;
  const active = Math.min(selected, Math.max(matches.length - 1, 0));

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setSelected((active + 1) % Math.max(matches.length, 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setSelected((active - 1 + matches.length) % Math.max(matches.length, 1));
    } else if (event.key === "Enter") {
      event.preventDefault();
      matches[active]?.run();
    }
  };

  const onOpen = (next: boolean) => {
    if (next) {
      setQuery("");
      setSelected(0);
    }
    onOpenChange(next);
  };

  let lastGroup = "";

  return (
    <Dialog onOpenChange={onOpen} open={open}>
      <DialogContent
        className="top-[18%] max-w-[calc(100%-2rem)] translate-y-0 gap-0 overflow-hidden p-0 sm:max-w-xl"
        showCloseButton={false}
      >
        <DialogTitle className="sr-only">Command menu</DialogTitle>
        <div className="flex items-center gap-2.5 border-b px-4">
          <Search
            aria-hidden="true"
            className="size-4 shrink-0 text-muted-foreground"
          />
          <input
            aria-activedescendant={
              matches[active] ? `${listId}-${matches[active].id}` : undefined
            }
            aria-controls={listId}
            aria-expanded="true"
            aria-label="Search commands"
            autoComplete="off"
            className="h-12 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            onChange={(event) => {
              setQuery(event.target.value);
              setSelected(0);
            }}
            onKeyDown={onKeyDown}
            placeholder="Search pages, organizations, actions…"
            role="combobox"
            value={query}
          />
          <Kbd>Esc</Kbd>
        </div>
        <div
          className="max-h-[min(60vh,420px)] overflow-y-auto p-2"
          id={listId}
          role="listbox"
        >
          {matches.length === 0 ? (
            <p className="py-10 text-center text-muted-foreground text-sm">
              No results for “{query}”
            </p>
          ) : null}
          {matches.map((command, index) => {
            const heading =
              command.group === lastGroup ? null : (
                <p className="px-2 pt-2.5 pb-1.5 font-medium text-muted-foreground text-xs first:pt-1">
                  {command.group}
                </p>
              );
            lastGroup = command.group;
            const Icon = command.icon;
            return (
              <div key={command.id}>
                {heading}
                <div
                  aria-selected={index === active}
                  className={cn(
                    "flex h-10 cursor-pointer items-center gap-3 rounded-md px-2 text-sm",
                    index === active
                      ? "bg-accent text-accent-foreground"
                      : "text-soft-foreground"
                  )}
                  id={`${listId}-${command.id}`}
                  onClick={command.run}
                  onKeyDown={undefined}
                  onMouseMove={() => setSelected(index)}
                  role="option"
                  tabIndex={-1}
                >
                  {Icon ? (
                    <Icon
                      aria-hidden="true"
                      className="size-4 shrink-0 text-muted-foreground"
                    />
                  ) : null}
                  {command.mark ? (
                    <OrgMark
                      className="size-4 text-[9px]"
                      login={command.mark}
                    />
                  ) : null}
                  <span className="truncate">{command.label}</span>
                  {command.shortcut ? (
                    <KbdGroup className="ml-auto">
                      {command.shortcut.map((key) => (
                        <Kbd key={key}>{key}</Kbd>
                      ))}
                    </KbdGroup>
                  ) : null}
                  {index === active && !command.shortcut ? (
                    <CornerDownLeft
                      aria-hidden="true"
                      className="ml-auto size-3.5 text-muted-foreground"
                    />
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
        <div className="flex items-center gap-4 border-t bg-muted/50 px-4 py-2 text-muted-foreground text-xs">
          <span className="flex items-center gap-1.5">
            <KbdGroup>
              <Kbd>↑</Kbd>
              <Kbd>↓</Kbd>
            </KbdGroup>
            Navigate
          </span>
          <span className="flex items-center gap-1.5">
            <Kbd>↵</Kbd>
            Open
          </span>
          <span className="ml-auto flex items-center gap-1.5">
            <KbdGroup>
              <Kbd>G</Kbd>
              <Kbd>D</Kbd>
            </KbdGroup>
            then a key, from any page
          </span>
        </div>
      </DialogContent>
    </Dialog>
  );
}
