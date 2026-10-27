"use client";

import { Button, buttonVariants } from "@airship/ui/components/button";
import { Skeleton } from "@airship/ui/components/skeleton";
import type { Route } from "next";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";

import { $api } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { safeNext } from "@/lib/navigation";

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
    <div className="grid min-h-svh place-items-center p-4">
      <div className="grid w-full max-w-xs gap-6">
        <div className="grid gap-2">
          <span className="font-medium font-mono text-sm tracking-tight">
            airship
          </span>
          <h1 className="font-medium text-xl tracking-tight">Sign in</h1>
          <p className="text-muted-foreground text-xs/relaxed">
            Fresh GitHub Actions runners in your own AWS or Google Cloud
            account, one per job, configured by a .cirun.yml.
          </p>
        </div>
        {failure ? (
          <p
            className="border border-destructive/30 bg-destructive/10 p-2 text-destructive text-xs"
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
                {errorMessage(config.error, "The API cannot be reached")}. Is
                the API running?
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
      </div>
    </div>
  );
}
