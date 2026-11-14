import type { Metadata } from "next";
import { Suspense } from "react";

import { Migrate } from "@/components/migrate";

export const metadata: Metadata = { title: "Move workflows" };

export default function Page() {
  return (
    <Suspense>
      <Migrate />
    </Suspense>
  );
}
