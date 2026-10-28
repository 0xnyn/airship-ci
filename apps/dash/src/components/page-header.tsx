"use client";

import { cn } from "@airship/ui/lib/utils";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";

import { useOrg } from "./org-context";
import { accentDot, currentSection } from "./sections";

/**
 * A page's title block, in the landing page's section voice: a mono eyebrow
 * with the section's dot, a tight sans heading, and a short description.
 */
export function PageHeader({
  action,
  description,
  title,
}: {
  action?: ReactNode;
  description?: ReactNode;
  title: string;
}) {
  const { org } = useOrg();
  const pathname = usePathname();
  const current = currentSection(pathname, `/${org.login}`);

  return (
    <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
      <div className="grid min-w-0 gap-2">
        {current ? (
          <p className="flex items-center gap-2 font-mono text-muted-foreground text-xs tracking-label">
            <span
              aria-hidden="true"
              className={cn(
                "h-2 w-3.5 shrink-0 rounded-full",
                accentDot[current.section.accent]
              )}
            />
            {current.group.label}
          </p>
        ) : null}
        <h1 className="text-balance font-medium text-[28px] leading-[1.1] tracking-display">
          {title}
        </h1>
        {description ? (
          <p className="max-w-2xl text-pretty text-[13px] text-muted-foreground leading-relaxed">
            {description}
          </p>
        ) : null}
      </div>
      {action ? <div className="flex shrink-0 gap-2">{action}</div> : null}
    </div>
  );
}

/** A heading inside a page, with an optional link or control on the right. */
export function SectionHeading({
  action,
  children,
  id,
}: {
  action?: ReactNode;
  children: ReactNode;
  id?: string;
}) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <h2 className="font-medium text-[15px] tracking-tight" id={id}>
        {children}
      </h2>
      {action}
    </div>
  );
}
