import {
  Activity,
  BarChart3,
  Cloud,
  FolderGit2,
  KeyRound,
  LayoutDashboard,
  ListChecks,
  type LucideIcon,
  Radio,
  Settings,
  Users,
} from "lucide-react";

import { isActiveSection } from "@/lib/navigation";

/**
 * The signal colour a section wears as its dot, as the landing page gives
 * each of its sections one. Class names rather than values so they follow
 * the theme; the colours themselves come from @airship/tokens.
 */
export const accentDot = {
  blue: "bg-active",
  cyan: "bg-info",
  gold: "bg-warning",
  green: "bg-success",
  white: "bg-foreground",
  yellow: "bg-highlight",
} as const;

export type Accent = keyof typeof accentDot;

export interface Section {
  accent: Accent;
  /** Path under the organization, "" for its home. */
  href: string;
  icon: LucideIcon;
  label: string;
}

export interface SectionGroup {
  label: string;
  sections: readonly Section[];
}

export const sectionGroups: readonly SectionGroup[] = [
  {
    label: "Fleet",
    sections: [
      { accent: "white", href: "", icon: LayoutDashboard, label: "Dashboard" },
      { accent: "blue", href: "/runners", icon: Activity, label: "Runners" },
      { accent: "green", href: "/jobs", icon: ListChecks, label: "Jobs" },
      {
        accent: "cyan",
        href: "/analytics",
        icon: BarChart3,
        label: "Analytics",
      },
      { accent: "gold", href: "/events", icon: Radio, label: "Events" },
    ],
  },
  {
    label: "Setup",
    sections: [
      {
        accent: "yellow",
        href: "/repositories",
        icon: FolderGit2,
        label: "Repositories",
      },
      { accent: "cyan", href: "/cloud", icon: Cloud, label: "Cloud" },
    ],
  },
  {
    label: "Organization",
    sections: [
      { accent: "green", href: "/teams", icon: Users, label: "Teams" },
      { accent: "gold", href: "/api-keys", icon: KeyRound, label: "API keys" },
      { accent: "white", href: "/settings", icon: Settings, label: "Settings" },
    ],
  },
];

/** The section and group the current path belongs to, if any. */
export function currentSection(
  pathname: string,
  base: string
): { group: SectionGroup; section: Section } | null {
  for (const group of sectionGroups) {
    for (const section of group.sections) {
      if (isActiveSection(pathname, base, section.href)) {
        return { group, section };
      }
    }
  }
  return null;
}
