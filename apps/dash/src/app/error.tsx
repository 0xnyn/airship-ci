"use client";

import { Button } from "@airship/ui/components/button";

import { AuthFrame, AuthHeading, authButton } from "@/components/auth-frame";

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
    <AuthFrame>
      <AuthHeading title="Something went wrong">
        {error.message || "This page could not be shown"}
      </AuthHeading>
      <Button className={authButton} onClick={reset} variant="outline">
        Try again
      </Button>
    </AuthFrame>
  );
}
