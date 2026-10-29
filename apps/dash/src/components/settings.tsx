"use client";

import { Button } from "@airship/ui/components/button";
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
import { Skeleton } from "@airship/ui/components/skeleton";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";
import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";
import { ErrorRow } from "./data-table";
import { DocsLink } from "./docs-link";

import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";

const SETTINGS = "/v1/orgs/{org}/settings";
const MIN_RUNNERS = 1;
const MAX_RUNNERS = 10_000;

export function SettingsPage() {
  const { org } = useOrg();
  const settings = $api.useQuery("get", SETTINGS, {
    params: { path: { org: org.login } },
  });

  return (
    <>
      <PageHeader
        description="Limits for the whole organization"
        title="Settings"
      />
      {settings.isError ? (
        <ErrorRow error={settings.error} fallback="Could not load settings" />
      ) : null}
      {settings.isPending ? <Skeleton className="h-24" /> : null}
      {settings.data ? (
        // Keyed on the stored value, so a change saved elsewhere resets
        // the form instead of leaving a stale number ready to save back.
        <MaxRunners
          initial={settings.data.maxRunners}
          key={settings.data.maxRunners}
        />
      ) : null}
    </>
  );
}

function MaxRunners({ initial }: { initial: number }) {
  const { isAdmin, org } = useOrg();
  const queryClient = useQueryClient();
  const [value, setValue] = useState(String(initial));
  const update = $api.useMutation("patch", SETTINGS, {
    onSuccess: async (saved) => {
      setValue(String(saved.maxRunners));
      toast.success(`Runner limit saved: ${saved.maxRunners}`);
      await invalidatePath(queryClient, SETTINGS);
    },
  });
  const parsed = Number.parseInt(value, 10);
  const changed = parsed !== initial;

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    update.mutate({
      body: { maxRunners: parsed },
      params: { path: { org: org.login } },
    });
  };

  return (
    <form
      className="grid max-w-md gap-4 rounded-lg border bg-card p-5"
      onSubmit={submit}
    >
      <Field data-invalid={update.isError || undefined}>
        <FieldLabel htmlFor="max-runners">Runner limit</FieldLabel>
        <Input
          aria-invalid={update.isError || undefined}
          className="w-32 font-mono"
          disabled={!isAdmin}
          id="max-runners"
          inputMode="numeric"
          max={MAX_RUNNERS}
          min={MIN_RUNNERS}
          onChange={(event) => setValue(event.target.value)}
          required
          type="number"
          value={value}
        />
        <FieldDescription>
          Live machines across every repository, idle pools included. Jobs past
          the limit wait for a slot. <DocsLink page="Capacity and limits" />
        </FieldDescription>
        {update.isError ? (
          <FieldError>
            {errorMessage(update.error, "Could not save settings")}
          </FieldError>
        ) : null}
      </Field>
      {isAdmin ? (
        <div>
          <Button
            disabled={
              update.isPending ||
              !changed ||
              Number.isNaN(parsed) ||
              parsed < MIN_RUNNERS ||
              parsed > MAX_RUNNERS
            }
            type="submit"
          >
            {update.isPending ? "Saving…" : "Save"}
          </Button>
        </div>
      ) : (
        <p className="text-muted-foreground text-xs">
          Only organization owners can change this
        </p>
      )}
    </form>
  );
}
