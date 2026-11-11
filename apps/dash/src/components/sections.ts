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

export interface Section {
  /** Path under the organization, "" for its home. */
  href: string;
  icon: LucideIcon;
  label: string;
  /** The key pressed after G to go here, e.g. "j" for G J. */
  shortcut: string;
}

export interface SectionGroup {
  label: string;
  sections: readonly Section[];
}

export const sectionGroups: readonly SectionGroup[] = [
  {
    label: "Fleet",
    sections: [
      { href: "", icon: LayoutDashboard, label: "Dashboard", shortcut: "d" },
      { href: "/runners", icon: Activity, label: "Runners", shortcut: "r" },
      { href: "/jobs", icon: ListChecks, label: "Jobs", shortcut: "j" },
      {
        href: "/analytics",
        icon: BarChart3,
        label: "Analytics",
        shortcut: "a",
      },
      { href: "/events", icon: Radio, label: "Events", shortcut: "e" },
    ],
  },
  {
    label: "Setup",
    sections: [
      {
        href: "/repositories",
        icon: FolderGit2,
        label: "Repositories",
        shortcut: "p",
      },
      { href: "/cloud", icon: Cloud, label: "Cloud", shortcut: "c" },
    ],
  },
  {
    label: "Organization",
    sections: [
      { href: "/teams", icon: Users, label: "Teams", shortcut: "t" },
      { href: "/api-keys", icon: KeyRound, label: "API keys", shortcut: "k" },
      { href: "/settings", icon: Settings, label: "Settings", shortcut: "s" },
    ],
  },
];

/** Every section, in navigation order. */
export const sections: readonly Section[] = sectionGroups.flatMap(
  (group) => group.sections
);

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
