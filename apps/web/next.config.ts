import { varlockNextConfigPlugin } from "@varlock/nextjs-integration/plugin";
import type { NextConfig } from "next";

const withVarlock = varlockNextConfigPlugin();

const nextConfig: NextConfig = {
  reactCompiler: true,
  transpilePackages: ["@airship/client", "@airship/ui"],
  typedRoutes: true,
};

export default withVarlock(nextConfig);
