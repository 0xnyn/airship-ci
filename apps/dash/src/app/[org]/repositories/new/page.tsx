import type { Metadata } from "next";
import { Suspense } from "react";

import { AddRepository } from "@/components/add-repository";

export const metadata: Metadata = { title: "Add repository" };

export default function Page() {
  return (
    <Suspense>
      <AddRepository />
    </Suspense>
  );
}
