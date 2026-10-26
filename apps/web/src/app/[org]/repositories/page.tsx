import type { Metadata } from "next";

import { Repositories } from "@/components/repositories";

export const metadata: Metadata = { title: "Repositories" };

export default function Page() {
  return <Repositories />;
}
