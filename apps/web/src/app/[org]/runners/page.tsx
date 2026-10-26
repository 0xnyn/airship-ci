import type { Metadata } from "next";

import { Runners } from "@/components/runners";

export const metadata: Metadata = { title: "Runners" };

export default function Page() {
  return <Runners />;
}
