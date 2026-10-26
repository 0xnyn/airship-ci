import type { Metadata } from "next";

import { RunnerConfig } from "@/components/runner-config";

export const metadata: Metadata = { title: "Runner config" };

export default function Page() {
  return <RunnerConfig />;
}
