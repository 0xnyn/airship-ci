import type { Metadata } from "next";
import { Suspense } from "react";

import { ConnectCloudFlow } from "@/components/connect-cloud";

export const metadata: Metadata = { title: "Connect cloud" };

export default function Page() {
  return (
    <Suspense>
      <ConnectCloudFlow />
    </Suspense>
  );
}
