// Every number on the pricing page lives here, with where it came from.
// Cloud machine prices and competitor list prices (Linux x64) were checked
// on PRICES_CHECKED; update them together.

export const PRICES_CHECKED = "October 2026";

/** Airship's own fee, per runner-minute, any machine size. */
export const AIRSHIP_FEE_PER_MIN = 0.002;
/** Minutes a month the free plan covers. */
export const FREE_MINUTES = 3000;

export const SIZES = [2, 4, 8, 16, 32] as const;
export type Size = (typeof SIZES)[number];

export type CloudId = "aws" | "gcp" | "azure";

export interface Cloud {
  /** The machine family the hourly prices are for. */
  family: string;
  id: CloudId;
  name: string;
  /** On-demand Linux price per hour for each size. */
  onDemandHourly: Record<Size, number>;
  /** The region the hourly prices are for. */
  region: string;
  source: string;
  /**
   * How far under on-demand spot typically runs. Spot prices move, so the
   * page treats this as an estimate and stays on the low side of what we saw.
   */
  spotDiscount: number;
}

export const clouds: Cloud[] = [
  {
    // m7i.large ... m7i.8xlarge. Spot m7i.xlarge: about $0.064-0.08/hr.
    family: "m7i",
    id: "aws",
    name: "AWS",
    onDemandHourly: {
      2: 0.1008,
      4: 0.2016,
      8: 0.4032,
      16: 0.8064,
      32: 1.6128,
    },
    region: "us-east-1",
    source: "https://aws.amazon.com/ec2/pricing/on-demand/",
    spotDiscount: 0.65,
  },
  {
    // n2-standard-2 ... n2-standard-32. Spot n2-standard-8: about
    // $0.13-0.18/hr against $0.3885.
    family: "n2-standard",
    id: "gcp",
    name: "Google Cloud",
    onDemandHourly: {
      2: 0.0971,
      4: 0.1942,
      8: 0.3885,
      16: 0.7769,
      32: 1.5539,
    },
    region: "us-central1",
    source: "https://cloud.google.com/compute/vm-instance-pricing",
    spotDiscount: 0.6,
  },
  {
    // D2s_v5 ... D32s_v5. Spot D8s_v5: about $0.081/hr against $0.384.
    family: "Dsv5",
    id: "azure",
    name: "Azure",
    onDemandHourly: {
      2: 0.096,
      4: 0.192,
      8: 0.384,
      16: 0.768,
      32: 1.536,
    },
    region: "East US",
    source:
      "https://azure.microsoft.com/en-us/pricing/details/virtual-machines/linux/",
    spotDiscount: 0.75,
  },
];

const cloudById = (id: CloudId): Cloud =>
  clouds.find((c) => c.id === id) ?? clouds[0];

export const cloudPerMin = (id: CloudId, size: Size, spot: boolean): number => {
  const cloud = cloudById(id);
  return (
    (cloud.onDemandHourly[size] / 60) * (spot ? 1 - cloud.spotDiscount : 1)
  );
};

export interface Competitor {
  id: string;
  name: string;
  note: string;
  /** Per-minute list price for each size. */
  perMin: Record<Size, number>;
  source: string;
}

const linear = (perVcpuMin: number): Record<Size, number> => ({
  2: perVcpuMin * 2,
  4: perVcpuMin * 4,
  8: perVcpuMin * 8,
  16: perVcpuMin * 16,
  32: perVcpuMin * 32,
});

export const competitors: Competitor[] = [
  {
    id: "github",
    name: "GitHub-hosted",
    note: "Larger runners above 2 vCPU.",
    perMin: { 2: 0.006, 4: 0.012, 8: 0.022, 16: 0.042, 32: 0.082 },
    source:
      "https://docs.github.com/en/billing/reference/actions-runner-pricing",
  },
  {
    id: "depot",
    name: "Depot",
    note: "Plus a $20 or $200 monthly plan.",
    perMin: linear(0.003),
    source: "https://depot.dev/docs/github-actions/runner-types",
  },
  {
    id: "blacksmith",
    name: "Blacksmith",
    note: "3,000 free minutes a month.",
    perMin: linear(0.002),
    source: "https://www.blacksmith.sh/pricing",
  },
  {
    id: "warpbuild",
    name: "WarpBuild",
    note: "Their managed runners, not BYOC.",
    perMin: linear(0.002),
    source: "https://www.warpbuild.com/pricing",
  },
  {
    id: "namespace",
    name: "Namespace",
    note: "Pay-as-you-go rate; $0.001 per unit prepaid on Team.",
    perMin: linear(0.0015),
    source: "https://namespace.so/pricing",
  },
];

export const money = (n: number): string =>
  n >= 100 ? `$${Math.round(n).toLocaleString("en-US")}` : `$${n.toFixed(2)}`;
