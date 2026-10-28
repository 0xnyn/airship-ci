// @ts-check
import { defineConfig } from "astro/config";

export default defineConfig({
  server: { port: 4000 },
  // Absolute URLs in the canonical and Open Graph tags are built from this.
  site: "https://airshipci.com",
});
