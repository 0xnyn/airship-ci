import { Shell } from "@/components/shell";

export default async function OrgLayout({
  children,
  params,
}: LayoutProps<"/[org]">) {
  const { org } = await params;
  return <Shell org={org}>{children}</Shell>;
}
