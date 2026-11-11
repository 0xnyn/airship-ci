import type { ReactNode } from "react";

/** A page's title block: a bold heading, a one-line description, and actions. */
export function PageHeader({
  action,
  description,
  title,
}: {
  action?: ReactNode;
  description?: ReactNode;
  title: string;
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
      <div className="grid min-w-0 gap-1.5">
        <h1 className="text-balance font-semibold text-[28px] leading-[1.15] tracking-[-0.03em]">
          {title}
        </h1>
        {description ? (
          <p className="max-w-2xl text-pretty text-muted-foreground text-sm leading-relaxed">
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
      <h2 className="font-semibold text-base tracking-tight" id={id}>
        {children}
      </h2>
      {action}
    </div>
  );
}
