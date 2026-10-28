"use client";

import { Button, buttonVariants } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import type { Route } from "next";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { safeNext } from "@/lib/navigation";

import { AuthFrame, AuthHeading } from "./auth-frame";

export function SignIn() {
  const router = useRouter();
  const params = useSearchParams();
  const next = safeNext(params.get("next"));
  const failure = params.get("error");
  const config = $api.useQuery("get", "/v1/auth/config");

  const devSignIn = $api.useMutation("post", "/v1/auth/dev-sign-in", {
    onError: (error) => {
      toast.error(errorMessage(error, "Development sign-in failed"));
    },
    onSuccess: () => {
      router.replace(next as Route);
    },
  });

  const githubUrl = config.data?.githubSignInUrl;

  return (
    <AuthFrame>
      <AuthHeading eyebrow="Sign in" title="Your CI, on your cloud.">
        Fresh GitHub Actions runners in your own AWS, Google Cloud or Azure
        account, one per job, configured by an airship.yaml.
      </AuthHeading>
      {failure ? (
        <p
          className="rounded-md border border-danger/25 bg-danger-surface p-3 text-danger-foreground text-xs"
          role="alert"
        >
          {failure}
        </p>
      ) : null}
      {config.isPending ? (
        <Skeleton className="h-9" />
      ) : (
        <div className="grid gap-2">
          {githubUrl ? (
            <a
              className={buttonVariants({ size: "lg" })}
              href={`${githubUrl}?next=${encodeURIComponent(next)}`}
            >
              Continue with GitHub
            </a>
          ) : null}
          {config.data?.devSignIn ? (
            <Button
              disabled={devSignIn.isPending}
              onClick={() => devSignIn.mutate({})}
              size="lg"
              variant="outline"
            >
              {devSignIn.isPending ? "Signing in…" : "Development sign-in"}
            </Button>
          ) : null}
          {config.isError ? (
            <p className="text-destructive text-xs" role="alert">
              {errorMessage(config.error, "The API cannot be reached")}. Is the
              API running?
            </p>
          ) : null}
          {githubUrl || config.data?.devSignIn || config.isError ? null : (
            <p className="text-muted-foreground text-xs">
              Sign-in is not configured. Set the GitHub App variables on the
              API, or enable DEV_LOGIN in development.
            </p>
          )}
        </div>
      )}
    </AuthFrame>
  );
}
