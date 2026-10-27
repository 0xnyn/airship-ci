"use client";

import { Button } from "@airship/ui/components/button";

/**
 * Catches a render error anywhere under the root layout, so a bad value
 * on one page shows this instead of a blank screen.
 */
export default function RouteError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  return (
    <main className="mx-auto grid max-w-md gap-4 p-10 text-center">
      <h1 className="font-medium text-lg">Something went wrong</h1>
      <p className="text-muted-foreground text-xs">
        {error.message || "This page could not be shown."}
      </p>
      <div>
        <Button onClick={reset} size="sm" variant="outline">
          Try again
        </Button>
      </div>
    </main>
  );
}
