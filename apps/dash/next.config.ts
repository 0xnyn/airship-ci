import { fileURLToPath } from "node:url";
import { varlockNextConfigPlugin } from "@varlock/nextjs-integration/plugin";
import type { NextConfig } from "next";

const withVarlock = varlockNextConfigPlugin();

const nextConfig: NextConfig = {
  // A self-contained server in .next/standalone, which the Dockerfile ships.
  // Tracing starts at the monorepo root so workspace packages are included.
  output: "standalone",
  outputFileTracingRoot: fileURLToPath(new URL("../..", import.meta.url)),
  reactCompiler: true,
  transpilePackages: ["@airship/client", "@airship/tokens", "@airship/ui"],
  typedRoutes: true,
};

export default withVarlock(nextConfig);
