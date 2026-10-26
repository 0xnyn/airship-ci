"use client";

import type { Schemas } from "@airship/client";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@airship/ui/components/alert-dialog";
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
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";

import { ENV } from "@/env";
import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { CodeBlock } from "./copy-button";
import { EmptyRow, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { RelativeTime } from "./time";

const API_TOKENS = "/v1/orgs/{org}/api-tokens";

/** Developer API tokens, for scripts that activate repositories. Admins only. */
export function APITokens() {
  const { org } = useOrg();
  const [creating, setCreating] = useState(false);
  const [secret, setSecret] = useState<Schemas["APITokenSecret"] | null>(null);
  const tokens = $api.useQuery("get", API_TOKENS, {
    params: { path: { org: org.login } },
  });
  const items = tokens.data?.items ?? [];

  let body: ReactNode;
  if (tokens.isPending) {
    body = <TableSkeleton />;
  } else if (tokens.isError) {
    body = (
      <EmptyRow>{errorMessage(tokens.error, "Could not list tokens")}</EmptyRow>
    );
  } else if (items.length === 0) {
    body = (
      <EmptyRow>
        No tokens. Create one to list, activate and deactivate repositories from
        a script.
      </EmptyRow>
    );
  } else {
    body = (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Token</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Last used</TableHead>
            <TableHead className="w-0">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((token) => (
            <TableRow key={token.id}>
              <TableCell className="font-medium">{token.name}</TableCell>
              <TableCell className="font-mono text-muted-foreground">
                {token.tokenHint}…
              </TableCell>
              <TableCell>
                <RelativeTime value={token.createdAt} />
              </TableCell>
              <TableCell>
                {token.lastUsedAt ? (
                  <RelativeTime value={token.lastUsedAt} />
                ) : (
                  <span className="text-muted-foreground">Never</span>
                )}
              </TableCell>
              <TableCell>
                <RevokeToken token={token} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    );
  }

  return (
    <div className="grid gap-3">
      <div className="flex items-start justify-between gap-4">
        <p className="text-muted-foreground text-xs">
          A token can activate any repository the app can see, which lets its
          config files launch runners. Treat it like a cloud credential.
        </p>
        <Button onClick={() => setCreating(true)} size="sm" variant="outline">
          <Plus />
          New token
        </Button>
      </div>
      {body}
      <CreateTokenDialog
        onCreated={(created) => {
          setCreating(false);
          setSecret(created);
        }}
        onOpenChange={setCreating}
        open={creating}
      />
      <TokenSecretDialog onClose={() => setSecret(null)} secret={secret} />
    </div>
  );
}

function CreateTokenDialog({
  onCreated,
  onOpenChange,
  open,
}: {
  onCreated: (secret: Schemas["APITokenSecret"]) => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const create = $api.useMutation("post", API_TOKENS, {
    onSuccess: async (created) => {
      setName("");
      onCreated(created);
      await invalidatePath(queryClient, API_TOKENS);
    },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    create.mutate({
      body: { name: name.trim() },
      params: { path: { org: org.login } },
    });
  };

  return (
    <Dialog
      onOpenChange={(next) => {
        create.reset();
        onOpenChange(next);
      }}
      open={open}
    >
      <DialogContent>
        <form className="grid gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>New API token</DialogTitle>
            <DialogDescription>
              The token is shown once, after you create it.
            </DialogDescription>
          </DialogHeader>
          <Field data-invalid={create.isError || undefined}>
            <FieldLabel htmlFor="api-token-name">Name</FieldLabel>
            <Input
              aria-invalid={create.isError || undefined}
              autoComplete="off"
              id="api-token-name"
              maxLength={63}
              onChange={(event) => setName(event.target.value)}
              placeholder="terraform"
              required
              value={name}
            />
            <FieldDescription>
              What uses it, so you know what breaks if you revoke it.
            </FieldDescription>
            {create.isError ? (
              <FieldError>
                {errorMessage(create.error, "Could not create the token")}
              </FieldError>
            ) : null}
          </Field>
          <DialogFooter>
            <Button
              disabled={create.isPending || name.trim() === ""}
              type="submit"
            >
              {create.isPending ? "Creating…" : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function TokenSecretDialog({
  onClose,
  secret,
}: {
  onClose: () => void;
  secret: Schemas["APITokenSecret"] | null;
}) {
  if (!secret) {
    return null;
  }
  const example = [
    `curl -X PUT ${ENV.NEXT_PUBLIC_API_URL}/v1/api/repositories/REPO/activation \\`,
    `  -H "Authorization: Bearer ${secret.token}" \\`,
    `  -H "Content-Type: application/json" -d '{}'`,
  ].join("\n");

  return (
    <Dialog onOpenChange={(open) => (open ? null : onClose())} open>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{secret.apiToken.name}</DialogTitle>
          <DialogDescription>
            This token is shown once. Airship only stores a hash of it; if you
            lose it, revoke it and create another.
          </DialogDescription>
        </DialogHeader>
        <CodeBlock label="Copy token" value={secret.token} />
        <p className="text-muted-foreground text-xs">
          Activate a repository (named without the owner):
        </p>
        <CodeBlock label="Copy command" value={example} />
      </DialogContent>
    </Dialog>
  );
}

function RevokeToken({ token }: { token: Schemas["APIToken"] }) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const revoke = $api.useMutation("delete", "/v1/orgs/{org}/api-tokens/{id}", {
    onError: (error) => {
      setOpen(false);
      toast.error(errorMessage(error, "Could not revoke the token"));
    },
    onSuccess: async () => {
      setOpen(false);
      toast.success(`Revoked ${token.name}`);
      await invalidatePath(queryClient, API_TOKENS);
    },
  });

  return (
    <>
      <Button onClick={() => setOpen(true)} size="sm" variant="ghost">
        Revoke
      </Button>
      <AlertDialog onOpenChange={setOpen} open={open}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke {token.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              Scripts using it are refused from their next request.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={revoke.isPending}
              onClick={() =>
                revoke.mutate({
                  params: { path: { id: token.id, org: org.login } },
                })
              }
              variant="destructive"
            >
              Revoke
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
