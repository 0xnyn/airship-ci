"use client";

import {
  Avatar,
  AvatarFallback,
  AvatarImage,
} from "@airship/ui/components/avatar";
import { Badge } from "@airship/ui/components/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { ArrowUpRight } from "lucide-react";

import { $api } from "@/lib/api";

import {
  EmptyRow,
  ErrorRow,
  Pagination,
  TableSkeleton,
  useClientPages,
} from "./data-table";
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
  const { pagination, rows } = useClientPages(items);

  return (
    <>
      <PageHeader
        action={
          <a
            className="inline-flex h-8 items-center gap-1 rounded-md px-2.5 text-muted-foreground text-sm transition-colors hover:bg-accent hover:text-foreground"
            href={`https://github.com/orgs/${org.login}/people`}
            rel="noopener noreferrer"
            target="_blank"
          >
            Manage on GitHub
            <ArrowUpRight aria-hidden="true" className="size-3.5" />
          </a>
        }
        description="Organization members who have signed in. Owners can change settings."
        title="Teams"
      />
      {members.isPending ? <TableSkeleton /> : null}
      {members.isError ? (
        <ErrorRow error={members.error} fallback="Could not load members" />
      ) : null}
      {members.isSuccess && items.length === 0 ? (
        <EmptyRow>Nobody has signed in yet</EmptyRow>
      ) : null}
      {rows.length > 0 ? (
        <Table className="text-sm" footer={<Pagination {...pagination} />}>
          <TableHeader>
            <TableRow>
              <TableHead>Member</TableHead>
              <TableHead>Role</TableHead>
              <TableHead className="hidden text-right sm:table-cell">
                Role checked
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((member) => (
              <TableRow key={member.login}>
                <TableCell>
                  <div className="flex min-w-0 items-center gap-3">
                    <Avatar className="size-7">
                      {member.avatarUrl ? (
                        <AvatarImage alt="" src={member.avatarUrl} />
                      ) : null}
                      <AvatarFallback className="text-[11px]">
                        {member.login.slice(0, 1).toUpperCase()}
                      </AvatarFallback>
                    </Avatar>
                    <span className="grid min-w-0 gap-0.5">
                      <span className="truncate font-medium">
                        {member.name ?? member.login}
                      </span>
                      <span className="truncate text-muted-foreground text-xs">
                        @{member.login}
                      </span>
                    </span>
                  </div>
                </TableCell>
                <TableCell>
                  <Badge
                    variant={member.role === "admin" ? "default" : "secondary"}
                  >
                    {roleLabel[member.role]}
                  </Badge>
                </TableCell>
                <TableCell className="hidden text-right text-muted-foreground sm:table-cell">
                  <RelativeTime value={member.refreshedAt} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
    </>
  );
}
