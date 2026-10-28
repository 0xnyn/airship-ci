import { theme } from "@airship/tokens/tokens";
import type { Metadata, Viewport } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import type { ReactNode } from "react";
import { ENV } from "@/env";

import "../index.css";
import { Providers } from "@/components/providers";

const geistSans = Geist({ subsets: ["latin"], variable: "--font-geist-sans" });
const geistMono = Geist_Mono({
  subsets: ["latin"],
  variable: "--font-geist-mono",
});

const description =
  "Every runner, job and minute across your AWS, Google Cloud and Azure accounts.";

// The icons and the social card are files beside this one (favicon.ico,
// icon.svg, apple-icon.png, opengraph-image.jpg), drawn from @airship/tokens;
// see packages/tokens/DESIGN.md. Next.js links each of them.
export const metadata: Metadata = {
  applicationName: "Airship",
  description,
  metadataBase: ENV.NEXT_PUBLIC_APP_URL
    ? new URL(ENV.NEXT_PUBLIC_APP_URL)
    : undefined,
  openGraph: {
    description,
    siteName: "Airship",
    title: "Airship dashboard",
    type: "website",
  },
  title: { default: "Airship", template: "%s · Airship" },
  twitter: {
    card: "summary_large_image",
    description,
    title: "Airship dashboard",
  },
};

// The browser chrome takes the sidebar's ground in each theme.
export const viewport: Viewport = {
  colorScheme: "dark light",
  themeColor: [
    { color: theme.light.sidebar, media: "(prefers-color-scheme: light)" },
    { color: theme.dark.sidebar, media: "(prefers-color-scheme: dark)" },
  ],
};

export default function RootLayout({
  children,
}: Readonly<{ children: ReactNode }>) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body
        className={`${geistSans.variable} ${geistMono.variable} antialiased`}
      >
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
