"use client";

import type { Schemas } from "@airship/client";
import { Button } from "@airship/ui/components/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@airship/ui/components/dialog";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldLabel,
} from "@airship/ui/components/field";
import { Switch } from "@airship/ui/components/switch";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { DocsLink } from "./docs-link";
import { useOrg } from "./org-context";

const REPOSITORIES = "/v1/orgs/{org}/repositories";
const ACTIVATION = "/v1/orgs/{org}/repositories/{repo}/activation";
const POOLS = "/v1/orgs/{org}/pools";

/** Confirms activating a repository, and whether it runs while public. */
export function ActivateDialog({
  onClose,
  repo,
}: {
  onClose: () => void;
  repo: Schemas["Repository"] | null;
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [allowPublic, setAllowPublic] = useState(false);
  const activate = $api.useMutation("put", ACTIVATION, {
    onError: (error) => {
      toast.error(errorMessage(error, "Could not activate the repository"));
    },
    onSuccess: async (activated) => {
      onClose();
      setAllowPublic(false);
      toast.success(
        activated.config?.error
          ? `Activated ${activated.fullName}, but its airship.yaml has a problem`
          : `Activated ${activated.fullName}`
      );
      await invalidatePath(queryClient, REPOSITORIES, POOLS);
    },
  });
  if (!repo) {
    return null;
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    activate.mutate({
      body: { allowPublic },
      params: { path: { org: org.login, repo: repo.id } },
    });
  };

  return (
    <Dialog onOpenChange={(open) => (open ? null : onClose())} open>
      <DialogContent>
        <form className="grid gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Activate {repo.fullName}?</DialogTitle>
            <DialogDescription>
              Jobs asking for an <code className="font-mono">airship-</code>{" "}
              label will launch machines in your cloud account, and any idle
              pools start right away. <DocsLink page="Security and trust" />
            </DialogDescription>
          </DialogHeader>
          <Field orientation="horizontal">
            <FieldContent>
              <FieldLabel htmlFor="allow-public">
                Run jobs while public
              </FieldLabel>
              <FieldDescription>
                {repo.private
                  ? "If this repository becomes public, Airship stops its jobs unless this is on"
                  : "This repository is public: anyone who opens a pull request can run code on your machines"}
              </FieldDescription>
            </FieldContent>
            <Switch
              checked={allowPublic}
              id="allow-public"
              onCheckedChange={setAllowPublic}
            />
          </Field>
          <DialogFooter>
            <Button disabled={activate.isPending} type="submit">
              {activate.isPending ? "Activating…" : "Activate"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
