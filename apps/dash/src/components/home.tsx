"use client";

import { buttonVariants } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import type { Route } from "next";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

import { $api } from "@/lib/api";

import { AuthFrame, AuthHeading } from "./auth-frame";
import { NotFound } from "./not-found";
import { useSession } from "./shell";

/** Sends the user to their first organization, or explains how to add one. */
export function Home() {
  const router = useRouter();
  const me = useSession();
  const first = me.data?.organizations?.[0];

  useEffect(() => {
    if (first) {
      router.replace(`/${first.login}` as Route);
    }
  }, [first, router]);

  if (me.isError && !me.signedOut) {
    return (
      <NotFound
        description="The Airship API did not answer. Check that it is running and try again."
        title="Could not load your account"
      />
    );
  }
  if (me.data && !first) {
    return <InstallApp />;
  }
  return (
    <div className="grid min-h-svh place-items-center">
      <Skeleton className="h-8 w-48" />
    </div>
  );
}

function InstallApp() {
  const config = $api.useQuery("get", "/v1/auth/config");
  const installUrl = config.data?.installUrl;

  return (
    <AuthFrame>
      <AuthHeading eyebrow="Get started" title="Install Airship on GitHub">
        Airship only sees organizations it is installed on.
      </AuthHeading>
      {installUrl ? (
        <a className={buttonVariants({ size: "lg" })} href={installUrl}>
          Install the GitHub App
        </a>
      ) : (
        <p className="text-muted-foreground text-xs">
          No GitHub App is configured for this Airship deployment
        </p>
      )}
    </AuthFrame>
  );
}
