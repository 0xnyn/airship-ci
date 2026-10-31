// What the pricing page says. The numbers themselves are in prices.ts.
import {
  AIRSHIP_FEE_PER_MIN,
  type CloudId,
  FREE_MINUTES,
  type Size,
} from "./prices";
import { appUrl, calUrl } from "./site";
import type { Heading, Link } from "./types";

const free = FREE_MINUTES.toLocaleString("en-US");

export interface Plan {
  audience: string;
  cta: Link;
  features: string[];
  highlight?: boolean;
  name: string;
  note: string;
  price: string;
  unit?: string;
}

export const pricing = {
  compare: {
    defaults: {
      cloud: "aws" as CloudId,
      credits: true,
      minutes: 100_000,
      size: 8 as Size,
      spot: true,
    },
    heading: {
      accent: "cyan",
      description: "Compare your monthly CI costs across providers.",
      label: "Compare",
      title: "What a month of CI costs",
    } satisfies Heading,
    labels: {
      airship: "Airship",
      chart: "Monthly cost by provider",
      cloud: "Cloud",
      credits: "Cloud credits",
      creditsOff: "Off",
      creditsOn: "On",
      machines: "Airship machines",
      minutes: "Minutes a month",
      onDemand: "On-demand",
      size: "Runner size",
      spot: "Spot",
    },
    legend: {
      /** {cloud} is the cloud picked in the calculator. */
      cloud: "Your {cloud} bill",
      credits: "Paid by your cloud credits",
      fee: "Airship fee",
      other: "List price",
    },
    minutePresets: [10_000, 50_000, 100_000, 250_000, 1_000_000],
    /** {saved} and {cheapest} are filled in by the calculator. */
    saving:
      "less than GitHub-hosted runners: {saved} a month saved, and {cheapest}% under the cheapest alternative.",
  },
  hero: {
    description:
      "Pay a flat fee per runner-minute. Use your existing cloud credits to cover compute costs.",
    label: "Pricing",
    title: "Simple, usage-based pricing.",
  },
  meta: {
    description: `Airship costs $${AIRSHIP_FEE_PER_MIN} per runner-minute. Compare it with GitHub-hosted runners, Blacksmith, Depot, WarpBuild and Namespace.`,
    title: "Pricing · Airship",
  },
  plans: [
    {
      audience: "For trying it on a repository",
      cta: { href: appUrl, label: "Start for free" },
      features: [
        `${free} runner minutes a month`,
        "AWS, Google Cloud and Azure",
        "Any machine size, spot or on-demand",
        "Cache in your own S3 bucket",
        "Community support",
      ],
      name: "Free",
      note: "No credit card",
      price: "$0",
    },
    {
      audience: "For teams running CI every day",
      cta: { href: appUrl, label: "Start for free" },
      features: [
        "Unlimited runner minutes",
        "Same price for 2 or 64 vCPU",
        "Linux, Windows, ARM and GPU runners",
        "Warm idle runners on a schedule",
        "Organization runners and access.yaml",
        "Developer API",
        "Email support",
      ],
      highlight: true,
      name: "Team",
      note: `First ${free} minutes each month free`,
      price: `$${AIRSHIP_FEE_PER_MIN}`,
      unit: "/runner-minute",
    },
    {
      audience: "For large fleets and strict networks",
      cta: { href: calUrl, label: "Talk to us" },
      features: [
        "Volume pricing",
        "Run the control plane yourself",
        "Private support channel",
        "Help with images, quotas and networking",
      ],
      name: "Enterprise",
      note: "Annual agreement",
      price: "Custom",
    },
  ] satisfies Plan[],
};
