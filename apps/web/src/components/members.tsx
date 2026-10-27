"use client";

import {
  Avatar,
  AvatarFallback,
  AvatarImage,
} from "@airship/ui/components/avatar";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";

import { $api } from "@/lib/api";

import { EmptyRow, ErrorRow, TableSkeleton } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";
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
      <PageHeader
        description="Everyone who has opened this dashboard. Access follows GitHub: organization owners can change settings, other members can look, and anyone removed from the organization loses access within the hour."
        title="Teams"
      />
      {members.isPending ? <TableSkeleton /> : null}
      {members.isError ? (
        <ErrorRow error={members.error} fallback="Could not load members" />
      ) : null}
      {members.isSuccess && items.length === 0 ? (
        <EmptyRow>Nobody has signed in yet.</EmptyRow>
      ) : null}
      {items.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Member</TableHead>
              <TableHead>Role</TableHead>
              <TableHead className="text-right">Checked with GitHub</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((member) => (
              <TableRow key={member.login}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <Avatar className="size-6">
                      {member.avatarUrl ? (
                        <AvatarImage alt="" src={member.avatarUrl} />
                      ) : null}
                      <AvatarFallback>
                        {member.login.slice(0, 1).toUpperCase()}
                      </AvatarFallback>
                    </Avatar>
                    <div className="grid gap-0.5">
                      <span className="font-medium">{member.login}</span>
                      {member.name ? (
                        <span className="text-muted-foreground">
                          {member.name}
                        </span>
                      ) : null}
                    </div>
                  </div>
                </TableCell>
                <TableCell>{roleLabel[member.role]}</TableCell>
                <TableCell className="text-right text-muted-foreground">
                  <RelativeTime value={member.refreshedAt} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
      <p className="max-w-prose text-muted-foreground text-xs/relaxed">
        To give someone access, add them to the GitHub organization. To let them
        change settings, make them an organization owner. Who may launch
        organization-wide runners is decided per repository in{" "}
        <code className="font-mono">.access.yml</code>.
      </p>
    </>
  );
}
