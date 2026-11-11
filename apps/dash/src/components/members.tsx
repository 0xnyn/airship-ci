"use client";

import {
  Avatar,
  AvatarFallback,
  AvatarImage,
} from "@airship/ui/components/avatar";
import { Badge } from "@airship/ui/components/badge";

import { $api } from "@/lib/api";

import { EmptyRow, ErrorRow, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
import { SettingsCard, SettingsCardFooter, SettingsCardHeader } from "./panel";
import { RelativeTime } from "./time";

const roleLabel = {
  admin: "Owner",
  member: "Member",
} as const;

export function Teams() {
  const { org } = useOrg();
  const members = $api.useQuery("get", "/v1/orgs/{org}/members", {
    params: { path: { org: org.login } },
  });
  const items = members.data?.items ?? [];

  return (
    <>
      <PageHeader description="Who can open this dashboard" title="Teams" />
      {members.isPending ? <TableSkeleton /> : null}
      {members.isError ? (
        <ErrorRow error={members.error} fallback="Could not load members" />
      ) : null}
      {members.isSuccess && items.length === 0 ? (
        <EmptyRow>Nobody has signed in yet</EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <SettingsCard aria-labelledby="members-title">
          <SettingsCardHeader
            description="Everyone in the GitHub organization who has signed in"
            id="members-title"
            title={
              <span className="flex items-center gap-2">
                Members
                <span className="figure rounded-full bg-muted px-1.5 font-normal text-muted-foreground text-xs">
                  {items.length}
                </span>
              </span>
            }
          />
          <ul className="border-t">
            {items.map((member) => (
              <li
                className="flex items-center gap-3 border-b px-5 py-3 last:border-b-0 md:px-6"
                key={member.login}
              >
                <Avatar className="size-8">
                  {member.avatarUrl ? (
                    <AvatarImage alt="" src={member.avatarUrl} />
                  ) : null}
                  <AvatarFallback>
                    {member.login.slice(0, 1).toUpperCase()}
                  </AvatarFallback>
                </Avatar>
                <div className="grid min-w-0 flex-1 gap-0.5">
                  <span className="truncate font-medium text-sm">
                    {member.name ?? member.login}
                  </span>
                  <span className="truncate text-muted-foreground text-xs">
                    @{member.login}
                  </span>
                </div>
                <span className="hidden text-muted-foreground text-xs sm:inline">
                  Role checked <RelativeTime value={member.refreshedAt} />
                </span>
                <Badge
                  variant={member.role === "admin" ? "default" : "secondary"}
                >
                  {roleLabel[member.role]}
                </Badge>
              </li>
            ))}
          </ul>
          <SettingsCardFooter note="Add someone to the GitHub organization to give them access, or make them an owner to let them change settings." />
        </SettingsCard>
      ) : null}
    </>
  );
}
