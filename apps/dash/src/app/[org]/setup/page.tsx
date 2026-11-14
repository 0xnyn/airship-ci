import type { Metadata } from "next";
import { Suspense } from "react";

import { Setup } from "@/components/setup";

export const metadata: Metadata = { title: "Set up" };

export default function Page() {
  return (
    <Suspense>
      <Setup />
    </Suspense>
  );
}
