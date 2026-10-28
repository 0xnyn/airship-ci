import { theme } from "@airship/tokens/tokens";
import type { MetadataRoute } from "next";

// What a browser shows when the dashboard is installed. The icons are
// rendered from @airship/tokens by `pnpm --filter @airship/home media`.
export default function manifest(): MetadataRoute.Manifest {
  return {
    background_color: theme.dark.sidebar,
    description:
      "Every runner, job and minute across your AWS, Google Cloud and Azure accounts.",
    display: "standalone",
    icons: [
      { sizes: "any", src: "/icon.svg", type: "image/svg+xml" },
      { sizes: "192x192", src: "/icon-192.png", type: "image/png" },
      { sizes: "512x512", src: "/icon-512.png", type: "image/png" },
      {
        purpose: "maskable",
        sizes: "512x512",
        src: "/icon-maskable-512.png",
        type: "image/png",
      },
    ],
    name: "Airship",
    short_name: "Airship",
    start_url: "/",
    theme_color: theme.dark.sidebar,
  };
}
