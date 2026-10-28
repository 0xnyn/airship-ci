import type { Metadata } from "next";

import { Teams } from "@/components/members";

export const metadata: Metadata = { title: "Teams" };

export default function Page() {
  return <Teams />;
}
