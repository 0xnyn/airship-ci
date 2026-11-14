import { cn } from "@airship/ui/lib/utils";
import { ArrowUpRight } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { DOCS_URL } from "@/lib/docs";

import { Logo } from "./logo";

/**
 * The frame for pages outside an organization (sign-in, install, errors):
 * a quiet top bar, one centred column, and a footer line. The same frame
 * for every one of them, so getting in reads as a single flow.
 */
export function AuthFrame({
  children,
  footer,
}: {
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div className="relative isolate flex min-h-svh flex-col bg-background">
      <div
        aria-hidden="true"
        className="mask-[radial-gradient(ellipse_60%_50%_at_50%_0%,black,transparent)] pointer-events-none absolute inset-x-0 top-0 -z-10 h-[520px] bg-halftone"
      />
      <header className="flex h-16 items-center justify-between px-5 md:px-8">
        <Link aria-label="Airship home" className="rounded-sm" href="/">
          <Logo />
        </Link>
        <a
          className="inline-flex items-center gap-1 text-muted-foreground text-sm transition-colors hover:text-foreground"
          href={DOCS_URL}
          rel="noopener noreferrer"
          target="_blank"
        >
          Docs
          <ArrowUpRight aria-hidden="true" className="size-3.5" />
        </a>
      </header>
      <main className="grid flex-1 place-items-center px-5 pt-8 pb-20">
        <div className="grid w-full max-w-[360px] gap-8">{children}</div>
      </main>
      <footer className="px-5 pb-8 text-center text-muted-foreground text-xs">
        {footer ?? "Run your CI in your cloud"}
      </footer>
    </div>
  );
}

/** The centred heading and lead of an AuthFrame page. */
export function AuthHeading({
  children,
  mark = false,
  title,
}: {
  children?: ReactNode;
  /** Show the Airship mark above the heading, as the sign-in page does. */
  mark?: boolean;
  title: string;
}) {
  return (
    <div className="grid justify-items-center gap-3 text-center">
      {mark ? (
        <span className="mb-2 grid size-12 place-items-center rounded-xl border bg-card shadow-xs">
          <Logo size={24} wordmark={false} />
        </span>
      ) : null}
      <h1 className="text-balance font-semibold text-[32px] leading-[1.1] tracking-[-0.04em]">
        {title}
      </h1>
      {children ? (
        <p className="text-pretty text-muted-foreground text-sm leading-relaxed">
          {children}
        </p>
      ) : null}
    </div>
  );
}

/** A full-width action in an AuthFrame, as Vercel-style sign-in stacks them. */
export const authButton = "h-11 w-full gap-2.5 text-[15px]";

/** A row of "what happens next", shown under an AuthFrame's actions. */
export function NextSteps({
  current,
  steps,
}: {
  current: number;
  steps: readonly string[];
}) {
  return (
    <ol className="grid gap-px overflow-hidden rounded-lg border bg-border text-sm">
      {steps.map((step, index) => (
        <li
          className={cn(
            "flex items-center gap-3 bg-card px-4 py-2.5",
            index !== current && "text-muted-foreground"
          )}
          key={step}
        >
          <span
            aria-hidden="true"
            className={cn(
              "figure grid size-5 shrink-0 place-items-center rounded-full border text-[11px]",
              index === current && "border-foreground text-foreground"
            )}
          >
            {index + 1}
          </span>
          {step}
          {index === current ? (
            <span className="sr-only"> (this step)</span>
          ) : null}
        </li>
      ))}
    </ol>
  );
}

/** GitHub's mark, drawn in the current colour. */
export function GitHubMark({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      className={cn("size-[18px] shrink-0", className)}
      fill="currentColor"
      viewBox="0 0 16 16"
    >
      <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
  );
}
