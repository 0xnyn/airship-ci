import type { Metadata } from "next";

import { CloudPage } from "@/components/cloud";

export const metadata: Metadata = { title: "Cloud" };

export default function Page() {
  return <CloudPage />;
}
