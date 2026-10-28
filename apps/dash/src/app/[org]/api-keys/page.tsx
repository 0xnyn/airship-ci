import type { Metadata } from "next";

import { APIKeys } from "@/components/api-tokens";

export const metadata: Metadata = { title: "API keys" };

export default function Page() {
  return <APIKeys />;
}
