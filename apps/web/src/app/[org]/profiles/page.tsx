import type { Metadata } from "next";

import { Profiles } from "@/components/profiles";

export const metadata: Metadata = { title: "Profiles" };

export default function Page() {
  return <Profiles />;
}
