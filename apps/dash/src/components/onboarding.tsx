import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import { ArrowLeft, Check } from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import type { ReactNode } from "react";

import { PageHeader } from "./page-header";
import { Panel } from "./panel";

/**
 * The frame every guided flow shares (setup, moving workflows): a back
 * link and title, a rail of steps on the left that can be visited in any
 * order, and the current step on the right. Nothing is forced; each step
 * can be skipped and come back to.
 */
export interface OnboardingStep<T extends string> {
  /** A step that cannot be opened yet, such as one needing a repository. */
  disabled?: boolean;
  /** Done steps show a check, whatever order they were done in. */
  done?: boolean;
  hint: string;
  id: T;
  title: string;
}

export function OnboardingLayout<T extends string>({
  aside,
  back,
  children,
  current,
  description,
  onVisit,
  steps,
  title,
}: {
  /** Shown under the steps, such as how progress is kept. */
  aside?: ReactNode;
  back: { href: string; label: string };
  children: ReactNode;
  current: T;
  description: string;
  onVisit: (step: T) => void;
  steps: readonly OnboardingStep<T>[];
  title: string;
}) {
  return (
    <>
      <div className="grid gap-3">
        <Link
          className="inline-flex w-fit items-center gap-1.5 text-muted-foreground text-sm transition-colors hover:text-foreground"
          href={back.href as Route}
        >
          <ArrowLeft aria-hidden="true" className="size-3.5" />
          {back.label}
        </Link>
        <PageHeader description={description} title={title} />
      </div>
      <div className="grid items-start gap-8 lg:grid-cols-[240px_minmax(0,1fr)]">
        <nav aria-label="Steps" className="grid gap-4 lg:sticky lg:top-6">
          <ol className="grid gap-1">
            {steps.map((step, index) => (
              <StepLink
                active={step.id === current}
                index={index}
                key={step.id}
                onVisit={() => onVisit(step.id)}
                step={step}
              />
            ))}
          </ol>
          {aside ? (
            <div className="px-2.5 text-muted-foreground text-xs">{aside}</div>
          ) : null}
        </nav>
        <div className="grid min-w-0 gap-6">{children}</div>
      </div>
    </>
  );
}

function StepLink<T extends string>({
  active,
  index,
  onVisit,
  step,
}: {
  active: boolean;
  index: number;
  onVisit: () => void;
  step: OnboardingStep<T>;
}) {
  return (
    <li>
      <button
        aria-current={active ? "step" : undefined}
        className={cn(
          "flex w-full items-start gap-3 rounded-md px-2.5 py-2 text-left transition-colors",
          active ? "bg-muted" : "hover:bg-muted/60",
          step.disabled && "pointer-events-none opacity-50"
        )}
        disabled={step.disabled}
        onClick={onVisit}
        type="button"
      >
        <span
          aria-hidden="true"
          className={cn(
            "figure mt-px grid size-5 shrink-0 place-items-center rounded-full border text-[11px] text-muted-foreground",
            active && "border-foreground text-foreground",
            step.done && "border-transparent bg-foreground text-background"
          )}
        >
          {step.done ? <Check className="size-3" /> : index + 1}
        </span>
        <span className="grid min-w-0 gap-0.5">
          <span
            className={cn(
              "font-medium text-sm",
              !(active || step.done) && "text-muted-foreground"
            )}
          >
            {step.title}
            <span className="sr-only">{step.done ? " (done)" : ""}</span>
          </span>
          <span className="truncate text-muted-foreground text-xs">
            {step.hint}
          </span>
        </span>
      </button>
    </li>
  );
}

/** A step's frame: heading, content, and a footer of navigation. */
export function StepPanel({
  children,
  description,
  footer,
  title,
}: {
  children: ReactNode;
  description: ReactNode;
  footer?: ReactNode;
  title: string;
}) {
  return (
    <section className="grid gap-4">
      <div className="grid gap-1">
        <h2 className="font-semibold text-xl tracking-[-0.02em]">{title}</h2>
        <p className="text-pretty text-muted-foreground text-sm">
          {description}
        </p>
      </div>
      {children}
      {footer ? (
        <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-4">
          {footer}
        </div>
      ) : null}
    </section>
  );
}

export function StepSkeleton() {
  return (
    <Panel className="grid gap-4 p-6">
      <Skeleton className="h-5 w-48" />
      <Skeleton className="h-4 w-72" />
      <Skeleton className="h-32" />
    </Panel>
  );
}
