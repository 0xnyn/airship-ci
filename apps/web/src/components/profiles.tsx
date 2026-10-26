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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@airship/ui/components/dropdown-menu";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldLabel,
} from "@airship/ui/components/field";
import { Input } from "@airship/ui/components/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@airship/ui/components/select";
import { Switch } from "@airship/ui/components/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { useQueryClient } from "@tanstack/react-query";
import { MoreHorizontal, Plus } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { toast } from "sonner";

import { $api, invalidatePath } from "@/lib/api";
import { errorMessage } from "@/lib/api-errors";

import { EmptyRow, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";

const PROFILES = "/v1/orgs/{org}/profiles";
const LABEL_PATTERN = "[a-z0-9][a-z0-9._\\-]*";
const DEFAULT_DISK_GB = 75;
const DEFAULT_MAX_RUNNERS = 20;

type Arch = Schemas["Profile"]["arch"];

const archOptions = [
  { label: "x64", value: "x64" },
  { label: "arm64", value: "arm64" },
] as const satisfies readonly { label: string; value: Arch }[];

export function Profiles() {
  const { isAdmin, org } = useOrg();
  const path = { params: { path: { org: org.login } } };
  const profiles = $api.useQuery("get", PROFILES, path);
  const dataPlanes = $api.useQuery("get", "/v1/orgs/{org}/data-planes", path);
  const [editing, setEditing] = useState<Schemas["Profile"] | "new" | null>(
    null
  );
  const planes = dataPlanes.data?.items ?? [];
  const items = profiles.data?.items ?? [];
  const planeName = new Map(planes.map((plane) => [plane.id, plane.name]));

  return (
    <>
      <PageHeader
        action={
          isAdmin ? (
            <Button
              disabled={planes.length === 0}
              onClick={() => setEditing("new")}
            >
              <Plus aria-hidden="true" />
              New profile
            </Button>
          ) : null
        }
        description={
          <>
            A profile maps a <code className="font-mono">runs-on</code> label to
            the machine a data plane starts for it. Jobs whose labels match
            exactly one profile run on Airship; everything else is left to
            GitHub.
          </>
        }
        title="Profiles"
      />
      {profiles.isPending ? <TableSkeleton /> : null}
      {profiles.isSuccess && items.length === 0 ? (
        <EmptyRow>
          {planes.length === 0
            ? "Create a data plane first; every profile runs on one."
            : "No profiles yet. Create one to start running jobs on Airship."}
        </EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Label</TableHead>
              <TableHead>Machine</TableHead>
              <TableHead>Data plane</TableHead>
              <TableHead className="text-right">Max runners</TableHead>
              <TableHead>Repositories</TableHead>
              {isAdmin ? (
                <TableHead className="w-10">
                  <span className="sr-only">Actions</span>
                </TableHead>
              ) : null}
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((profile) => (
              <TableRow key={profile.id}>
                <TableCell className="font-medium font-mono text-[11px]">
                  {profile.label}
                </TableCell>
                <TableCell>
                  <div className="grid gap-0.5">
                    <span className="font-mono text-[11px]">
                      {profile.instanceType} · {profile.arch}
                    </span>
                    <span className="text-muted-foreground">
                      {profile.diskGb} GB disk{profile.spot ? " · spot" : ""}
                      {profile.image ? ` · ${profile.image}` : ""}
                    </span>
                  </div>
                </TableCell>
                <TableCell>
                  {planeName.get(profile.dataPlaneId) ?? "—"}
                </TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {profile.maxRunners}
                </TableCell>
                <TableCell>
                  {profile.allowPublicRepos
                    ? "Private and public"
                    : "Private only"}
                </TableCell>
                {isAdmin ? (
                  <TableCell>
                    <ProfileActions
                      onEdit={() => setEditing(profile)}
                      profile={profile}
                    />
                  </TableCell>
                ) : null}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
      {editing ? (
        <ProfileDialog
          dataPlanes={planes}
          onClose={() => setEditing(null)}
          profile={editing === "new" ? null : editing}
        />
      ) : null}
    </>
  );
}

function ProfileActions({
  onEdit,
  profile,
}: {
  onEdit: () => void;
  profile: Schemas["Profile"];
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const remove = $api.useMutation("delete", "/v1/orgs/{org}/profiles/{id}", {
    onError: (error) => {
      setConfirming(false);
      toast.error(errorMessage(error, "Could not delete the profile"));
    },
    onSuccess: async () => {
      setConfirming(false);
      toast.success(`Deleted ${profile.label}`);
      await invalidatePath(queryClient, PROFILES);
    },
  });

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              aria-label={`Actions for ${profile.label}`}
              size="icon-sm"
              variant="ghost"
            />
          }
        >
          <MoreHorizontal />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={onEdit}>Edit</DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => setConfirming(true)}
            variant="destructive"
          >
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <AlertDialog onOpenChange={setConfirming} open={confirming}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete {profile.label}?</AlertDialogTitle>
            <AlertDialogDescription>
              Jobs asking for <code className="font-mono">{profile.label}</code>{" "}
              will wait in GitHub's queue until another runner picks them up. A
              profile with active runners cannot be deleted.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={remove.isPending}
              onClick={() =>
                remove.mutate({
                  params: { path: { id: profile.id, org: org.login } },
                })
              }
              variant="destructive"
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

interface FormState {
  allowPublicRepos: boolean;
  arch: Arch;
  dataPlaneId: string;
  diskGb: string;
  image: string;
  instanceType: string;
  label: string;
  maxRunners: string;
  spot: boolean;
}

function initialState(
  profile: Schemas["Profile"] | null,
  dataPlanes: Schemas["DataPlane"][]
): FormState {
  return {
    allowPublicRepos: profile?.allowPublicRepos ?? false,
    arch: profile?.arch ?? "x64",
    dataPlaneId: profile?.dataPlaneId ?? dataPlanes[0]?.id ?? "",
    diskGb: String(profile?.diskGb ?? DEFAULT_DISK_GB),
    image: profile?.image ?? "",
    instanceType: profile?.instanceType ?? "",
    label: profile?.label ?? "",
    maxRunners: String(profile?.maxRunners ?? DEFAULT_MAX_RUNNERS),
    spot: profile?.spot ?? false,
  };
}

const machineHelp: Record<
  string,
  { description: string; placeholder: string }
> = {
  aws: {
    description: "An EC2 instance type of the chosen architecture.",
    placeholder: "c7i.xlarge",
  },
  docker: {
    description: "Container limits as <cpus>cpu-<memory>gb.",
    placeholder: "4cpu-16gb",
  },
};

function ProfileDialog({
  dataPlanes,
  onClose,
  profile,
}: {
  dataPlanes: Schemas["DataPlane"][];
  onClose: () => void;
  profile: Schemas["Profile"] | null;
}) {
  const { org } = useOrg();
  const queryClient = useQueryClient();
  const [form, setForm] = useState(() => initialState(profile, dataPlanes));
  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((current) => ({ ...current, [key]: value }));

  const onSuccess = async (saved: Schemas["Profile"]) => {
    toast.success(
      profile ? `Updated ${saved.label}` : `Created ${saved.label}`
    );
    onClose();
    await invalidatePath(queryClient, PROFILES);
  };
  const create = $api.useMutation("post", PROFILES, { onSuccess });
  const update = $api.useMutation("patch", "/v1/orgs/{org}/profiles/{id}", {
    onSuccess,
  });
  const mutation = profile ? update : create;

  const provider =
    dataPlanes.find((plane) => plane.id === form.dataPlaneId)?.provider ??
    "aws";
  const help = machineHelp[provider] ?? machineHelp.aws;
  const planeItems = dataPlanes.map((plane) => ({
    label: plane.name,
    value: plane.id,
  }));

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const shared = {
      allowPublicRepos: form.allowPublicRepos,
      diskGb: Number(form.diskGb),
      image: form.image.trim(),
      instanceType: form.instanceType.trim(),
      maxRunners: Number(form.maxRunners),
      spot: form.spot,
    };
    if (profile) {
      update.mutate({
        body: shared,
        params: { path: { id: profile.id, org: org.login } },
      });
      return;
    }
    // On create a missing image means the default; an empty one is invalid.
    const { image, ...rest } = shared;
    create.mutate({
      body: {
        ...rest,
        ...(image ? { image } : {}),
        arch: form.arch,
        dataPlaneId: form.dataPlaneId,
        label: form.label.trim(),
      },
      params: { path: { org: org.login } },
    });
  };

  return (
    <Dialog onOpenChange={(open) => (open ? null : onClose())} open>
      <DialogContent className="sm:max-w-lg">
        <form className="grid gap-4" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>
              {profile ? `Edit ${profile.label}` : "New profile"}
            </DialogTitle>
            <DialogDescription>
              {profile
                ? "Changes apply to runners started from now on."
                : "Jobs use it with runs-on set to its label."}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <FormField
              description="What workflows put in runs-on."
              htmlFor="profile-label"
              label="Label"
            >
              <Input
                autoComplete="off"
                disabled={profile !== null}
                id="profile-label"
                maxLength={63}
                onChange={(event) =>
                  set("label", event.target.value.toLowerCase())
                }
                pattern={LABEL_PATTERN}
                placeholder="airship-4cpu"
                required
                value={form.label}
              />
            </FormField>
            <FormField label="Data plane">
              <Select
                disabled={profile !== null}
                items={planeItems}
                onValueChange={(value) => set("dataPlaneId", String(value))}
                value={form.dataPlaneId}
              >
                <SelectTrigger aria-label="Data plane" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {planeItems.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="Architecture">
              <Select
                disabled={profile !== null}
                items={archOptions}
                onValueChange={(value) =>
                  set("arch", value === "arm64" ? "arm64" : "x64")
                }
                value={form.arch}
              >
                <SelectTrigger aria-label="Architecture" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {archOptions.map((item) => (
                    <SelectItem key={item.value} value={item.value}>
                      {item.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </FormField>
            <FormField
              description={help?.description}
              htmlFor="profile-machine"
              label="Machine"
            >
              <Input
                autoComplete="off"
                id="profile-machine"
                maxLength={64}
                onChange={(event) => set("instanceType", event.target.value)}
                placeholder={help?.placeholder}
                required
                value={form.instanceType}
              />
            </FormField>
            <FormField htmlFor="profile-disk" label="Disk (GB)">
              <Input
                id="profile-disk"
                inputMode="numeric"
                max={16_384}
                min={8}
                onChange={(event) => set("diskGb", event.target.value)}
                required
                type="number"
                value={form.diskGb}
              />
            </FormField>
            <FormField
              description="Runners this profile may have at once."
              htmlFor="profile-max"
              label="Max runners"
            >
              <Input
                id="profile-max"
                inputMode="numeric"
                max={1000}
                min={1}
                onChange={(event) => set("maxRunners", event.target.value)}
                required
                type="number"
                value={form.maxRunners}
              />
            </FormField>
          </div>
          <FormField
            description={
              provider === "docker"
                ? "Container image; defaults to the official actions runner image."
                : "AMI ID or resolve:ssm:<parameter>; defaults to Ubuntu 24.04."
            }
            htmlFor="profile-image"
            label="Image"
          >
            <Input
              autoComplete="off"
              id="profile-image"
              maxLength={256}
              onChange={(event) => set("image", event.target.value)}
              placeholder="Default"
              value={form.image}
            />
          </FormField>
          {provider === "aws" ? (
            <SwitchField
              checked={form.spot}
              description="Cheaper, and Airship falls back to on-demand when spot capacity is short."
              id="profile-spot"
              label="Use spot instances"
              onChange={(checked) => set("spot", checked)}
            />
          ) : null}
          <SwitchField
            checked={form.allowPublicRepos}
            description="Anyone can open a pull request against a public repository and run code on these machines. Leave off unless you trust every contributor's workflow runs."
            id="profile-public"
            label="Allow public repositories"
            onChange={(checked) => set("allowPublicRepos", checked)}
          />
          {mutation.isError ? (
            <FieldError>
              {errorMessage(mutation.error, "Could not save the profile")}
            </FieldError>
          ) : null}
          <DialogFooter>
            <Button disabled={mutation.isPending} type="submit">
              {mutation.isPending ? "Saving…" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function FormField({
  children,
  description,
  htmlFor,
  label,
}: {
  children: ReactNode;
  description?: string;
  htmlFor?: string;
  label: string;
}) {
  return (
    <Field>
      <FieldLabel htmlFor={htmlFor}>{label}</FieldLabel>
      {children}
      {description ? <FieldDescription>{description}</FieldDescription> : null}
    </Field>
  );
}

function SwitchField({
  checked,
  description,
  id,
  label,
  onChange,
}: {
  checked: boolean;
  description: string;
  id: string;
  label: string;
  onChange: (checked: boolean) => void;
}) {
  return (
    <Field orientation="horizontal">
      <FieldContent>
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        <FieldDescription>{description}</FieldDescription>
      </FieldContent>
      <Switch checked={checked} id={id} onCheckedChange={onChange} />
    </Field>
  );
}
