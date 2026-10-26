import type { Metadata } from "next";

import { DataPlanes } from "@/components/data-planes";

export const metadata: Metadata = { title: "Data planes" };

export default function Page() {
  return <DataPlanes />;
}
