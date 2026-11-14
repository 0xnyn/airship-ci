"use client";

import { Button } from "@airship/ui/components/button";
import { Kbd } from "@airship/ui/components/kbd";
import { Skeleton } from "@airship/ui/components/skeleton";
import { cn } from "@airship/ui/lib/utils";
import { Check, X } from "lucide-react";
import type { Route } from "next";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { type ReactNode, useEffect } from "react";

import { Logo } from "./logo";
import { useOrg } from "./org-context";
import { OrgMark } from "./org-mark";
import { Panel } from "./panel";
import { Slash } from "./slash";

/**
 * The frame every guided flow shares (setup, adding a repository,
 * connecting a cloud, moving workflows). It takes the whole window: a slim
 * header with the way out, the steps on the left, and the current step on
 * the right. Steps can be visited in any order; nothing is forced.
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

/** Whether a key press belongs to a field, a dialog or a menu. */
function busyElsewhere(target: EventTarget | null): boolean {
  if (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      ["INPUT", "SELECT", "TEXTAREA"].includes(target.tagName))
  ) {
    return true;
  }
  return (
    document.querySelector(
      '[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]'
    ) !== null
  );
}

/** Esc leaves the flow, unless a field, dialog or menu has it. */
function useEscapeTo(href: string) {
  const router = useRouter();
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (
        event.key !== "Escape" ||
        event.defaultPrevented ||
        busyElsewhere(event.target)
      ) {
        return;
      }
      router.push(href as Route);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [href, router]);
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
  /** Where closing the flow goes. */
  back: { href: string; label: string };
  children: ReactNode;
  current: T;
  description: string;
  onVisit: (step: T) => void;
  steps: readonly OnboardingStep<T>[];
  title: string;
}) {
  const { org } = useOrg();
  useEscapeTo(back.href);
  const index = Math.max(
    steps.findIndex((step) => step.id === current),
    0
  );
  const progress = ((index + 1) / Math.max(steps.length, 1)) * 100;

  return (
    <>
      <header className="sticky top-0 z-30 border-b bg-background/85 backdrop-blur-md">
        <div className="flex h-14 items-center gap-1.5 px-4 md:px-6">
          <Link
            aria-label="Airship"
            className="hidden shrink-0 text-foreground sm:inline-flex"
            href={`/${org.login}` as Route}
          >
            <Logo size={16} wordmark={false} />
          </Link>
          <span className="hidden sm:contents">
            <Slash />
          </span>
          <Link
            className="flex min-w-0 items-center gap-2 rounded-md px-1.5 py-1 font-medium text-sm transition-colors hover:bg-accent"
            href={`/${org.login}` as Route}
          >
            <OrgMark className="size-4 text-[9px]" login={org.login} />
            <span className="truncate">{org.login}</span>
          </Link>
          <Slash />
          <span
            aria-current="page"
            className="truncate px-1.5 text-muted-foreground text-sm"
          >
            {title}
          </span>
          <div className="ml-auto flex items-center gap-2">
            <span className="hidden items-center gap-1.5 text-muted-foreground text-xs md:flex">
              <Kbd>Esc</Kbd>
              to close
            </span>
            <Button
              aria-label={`Close and go to ${back.label}`}
              className="text-muted-foreground"
              nativeButton={false}
              render={<Link href={back.href as Route} />}
              size="icon-sm"
              title={`Back to ${back.label}`}
              variant="ghost"
            >
              <X />
            </Button>
          </div>
        </div>
        <div aria-hidden="true" className="h-px bg-transparent">
          <div
            className="h-px bg-foreground transition-[width] duration-500 ease-smooth"
            style={{ width: `${progress}%` }}
          />
        </div>
      </header>

      <div className="mx-auto grid w-full max-w-6xl items-start gap-8 px-4 pt-8 pb-24 md:px-10 md:pt-12 lg:grid-cols-[256px_minmax(0,1fr)] lg:gap-16">
        <div className="grid gap-6 lg:sticky lg:top-24">
          <div className="grid gap-1.5">
            <h1 className="text-balance font-semibold text-2xl tracking-[-0.03em]">
              {title}
            </h1>
            <p className="text-pretty text-muted-foreground text-sm leading-relaxed">
              {description}
            </p>
          </div>
          <nav aria-label="Steps">
            <p className="mb-2 font-medium text-muted-foreground text-xs lg:hidden">
              Step {index + 1} of {steps.length}
            </p>
            <ol className="flex gap-1 overflow-x-auto lg:grid lg:gap-0 lg:overflow-visible">
              {steps.map((step, position) => (
                <StepLink
                  active={step.id === current}
                  index={position}
                  key={step.id}
                  last={position === steps.length - 1}
                  onVisit={() => onVisit(step.id)}
                  step={step}
                />
              ))}
            </ol>
          </nav>
          {aside ? (
            <div className="text-pretty text-muted-foreground text-xs leading-relaxed">
              {aside}
            </div>
          ) : null}
        </div>
        <div className="grid min-w-0 gap-6">{children}</div>
      </div>
    </>
  );
}

function StepLink<T extends string>({
  active,
  index,
  last,
  onVisit,
  step,
}: {
  active: boolean;
  index: number;
  last: boolean;
  onVisit: () => void;
  step: OnboardingStep<T>;
}) {
  return (
    <li className="relative shrink-0 lg:pb-1">
      {last ? null : (
        <span
          aria-hidden="true"
          className={cn(
            "absolute top-8 bottom-0 left-[19px] hidden w-px lg:block",
            step.done ? "bg-foreground/40" : "bg-border"
          )}
        />
      )}
      <button
        aria-current={active ? "step" : undefined}
        className={cn(
          "relative flex w-full items-start gap-3 rounded-md px-2.5 py-2 text-left outline-none transition-colors focus-visible:ring-1 focus-visible:ring-ring",
          active ? "bg-muted" : "hover:bg-muted/60",
          step.disabled && "pointer-events-none opacity-45"
        )}
        disabled={step.disabled}
        onClick={onVisit}
        type="button"
      >
        <span
          aria-hidden="true"
          className={cn(
            "figure mt-px grid size-5 shrink-0 place-items-center rounded-full border bg-background text-[11px] text-muted-foreground transition-colors",
            active && "border-foreground text-foreground",
            step.done && "border-transparent bg-foreground text-background"
          )}
        >
          {step.done ? <Check className="size-3" strokeWidth={3} /> : index + 1}
        </span>
        <span className="grid min-w-0 gap-0.5">
          <span
            className={cn(
              "whitespace-nowrap font-medium text-sm",
              !(active || step.done) && "text-muted-foreground"
            )}
          >
            {step.title}
            <span className="sr-only">{step.done ? " (done)" : ""}</span>
          </span>
          <span className="hidden truncate text-muted-foreground text-xs lg:block">
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
    <section className="grid gap-5">
      <div className="grid gap-1">
        <h2 className="font-semibold text-xl tracking-[-0.02em]">{title}</h2>
        <p className="text-pretty text-muted-foreground text-sm">
          {description}
        </p>
      </div>
      {children}
      {footer ? <StepFooter>{footer}</StepFooter> : null}
    </section>
  );
}

/**
 * The bar of Back / Skip / Continue under a step. It sticks to the bottom
 * of the window so the way forward stays in reach on a long step.
 */
export function StepFooter({ children }: { children: ReactNode }) {
  return (
    <div className="sticky bottom-0 z-10 -mx-1 mt-2 flex flex-wrap items-center justify-between gap-3 border-t bg-background/90 px-1 py-4 backdrop-blur-md">
      {children}
    </div>
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
