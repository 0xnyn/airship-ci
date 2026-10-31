import type { ReactNode } from "react";

import { GrainField } from "./grain-field";
import { Logo } from "./logo";

/**
 * The frame for pages outside an organization (sign-in, install, errors):
 * the content on the left, the landing page's dawn beam on the right.
 */
export function AuthFrame({ children }: { children: ReactNode }) {
  return (
    <div className="grid min-h-svh bg-sidebar lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
      <div className="relative flex flex-col px-6 py-8 md:px-12">
        <div
          aria-hidden="true"
          className="mask-[radial-gradient(ellipse_70%_60%_at_30%_40%,black,transparent)] pointer-events-none absolute inset-0 bg-halftone"
        />
        <Logo className="relative" />
        <div className="relative grid flex-1 place-items-center py-12">
          <div className="grid w-full max-w-sm gap-6">{children}</div>
        </div>
        <p className="relative font-mono text-[11px] text-muted-foreground">
          Run your CI in your cloud.
        </p>
      </div>
      <div className="relative hidden overflow-hidden lg:block lg:p-(--shell-inset) lg:pl-0">
        <div className="relative size-full overflow-hidden rounded-xl border border-white/10">
          <GrainField angle={-28} focus={0.6} id="auth" />
        </div>
      </div>
    </div>
  );
}

/** The eyebrow, heading and lead of an AuthFrame page. */
export function AuthHeading({
  children,
  eyebrow,
  title,
}: {
  children?: ReactNode;
  eyebrow: string;
  title: string;
}) {
  return (
    <div className="grid gap-3">
      <p className="flex items-center gap-2 font-mono text-muted-foreground text-xs tracking-label">
        <span
          aria-hidden="true"
          className="h-2 w-3.5 rounded-full bg-foreground"
        />
        {eyebrow}
      </p>
      <h1 className="text-balance font-medium text-[32px] leading-[1.08] tracking-display">
        {title}
      </h1>
      {children ? (
        <p className="text-pretty text-[13px] text-muted-foreground leading-relaxed">
          {children}
        </p>
      ) : null}
    </div>
  );
}
