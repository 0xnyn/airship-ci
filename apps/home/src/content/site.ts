import type { Link } from "./types";

// Where the site sends people. Override at build time with PUBLIC_APP_URL /
// PUBLIC_DOCS_URL / PUBLIC_API_URL.
export const siteUrl = "https://airshipci.com";
export const appUrl: string =
  import.meta.env.PUBLIC_APP_URL ?? "https://app.airshipci.com";
export const docsUrl: string =
  import.meta.env.PUBLIC_DOCS_URL ?? "https://docs.airshipci.com";
export const apiUrl: string =
  import.meta.env.PUBLIC_API_URL ?? "https://api.airshipci.com";

export const site = {
  description:
    "Airship launches a fresh machine in your AWS, Google Cloud or Azure account for every GitHub Actions job, from one airship.yaml per repository.",
  name: "Airship",
  tagline: "Run your CI in your cloud",
};

// The social card: rendered from og/card.html by `pnpm og`, served from
// public/og. Keep the size in step with render.mjs.
export const og = {
  height: 630,
  image: "/og/og.jpg",
  imageAlt:
    "Airship: Your CI, on your cloud. A runner list with jobs launching, busy and finished.",
  twitter: "summary_large_image",
  video: "/og/og.mp4",
  width: 1200,
};

export const nav = {
  cta: { href: appUrl, label: "Start for free" } satisfies Link,
  links: [
    { href: "/#features", label: "Features" },
    { href: "/#how", label: "How it works" },
    { href: "/#security", label: "Security" },
    { href: "/pricing", label: "Pricing" },
    { href: docsUrl, label: "Docs" },
  ] satisfies Link[],
};

export interface FooterColumn {
  links: Link[];
  title?: string;
}

export const footer = {
  columns: [
    {
      links: [
        { href: "/pricing", label: "Pricing" },
        { href: docsUrl, label: "Docs" },
        { href: appUrl, label: "Dashboard" },
      ],
    },
  ] as FooterColumn[],
  legal:
    "Airship. Runners launch in your cloud account and are billed by your provider.",
};
