import type { ReactNode } from "react";

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
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div className="grid gap-1">
        <h1 className="font-medium text-xl tracking-tight">{title}</h1>
        {description ? (
          <p className="max-w-prose text-muted-foreground text-xs/relaxed">
            {description}
          </p>
        ) : null}
      </div>
      {action}
    </div>
  );
}
