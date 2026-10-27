"use client";

import type { Schemas } from "@airship/client";
import { Skeleton } from "@airship/ui/components/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@airship/ui/components/table";
import { type PointerEvent, useId, useState } from "react";

import { $api } from "@/lib/api";
import { formatUTCDay, utcDayKey } from "@/lib/day-format";
import { formatDuration, formatInteger } from "@/lib/format";

import { EmptyRow, ErrorRow, FilterTabs } from "./data-table";
import { useOrg } from "./org-context";
import { PageHeader } from "./page-header";

type Window = "7" | "30" | "90";

const windows = [
  { label: "7 days", value: "7" },
  { label: "30 days", value: "30" },
  { label: "90 days", value: "90" },
] as const satisfies readonly { label: string; value: Window }[];

const DAY_MS = 24 * 60 * 60 * 1000;

export function AnalyticsPage() {
  const { org } = useOrg();
  const [window, setWindow] = useState<Window>("30");
  const analytics = $api.useQuery("get", "/v1/orgs/{org}/analytics", {
    params: { path: { org: org.login }, query: { days: Number(window) } },
  });

  return (
    <>
      <PageHeader
        description="How much ran on Airship, day by day, and which repositories used it."
        title="Analytics"
      />
      <FilterTabs
        label="Window"
        onChange={setWindow}
        options={windows}
        value={window}
      />
      {analytics.isPending ? <Skeleton className="h-64" /> : null}
      {analytics.isError ? (
        <ErrorRow error={analytics.error} fallback="Could not load analytics" />
      ) : null}
      {analytics.data ? (
        <Report days={Number(window)} report={analytics.data} />
      ) : null}
    </>
  );
}

/** One day on the x axis, with every measure the API reports. */
type Day = Schemas["DailyUsage"] & { otherJobs: number };

/** Fills the window with a row per day so quiet days show as gaps, not
 * missing columns. */
function fillDays(daily: Schemas["DailyUsage"][], days: number): Day[] {
  const byDay = new Map(
    daily.map((row) => [utcDayKey(new Date(row.day)), row])
  );
  const today = new Date();
  today.setUTCHours(0, 0, 0, 0);
  const out: Day[] = [];
  for (let offset = days - 1; offset >= 0; offset -= 1) {
    const date = new Date(today.getTime() - offset * DAY_MS);
    const key = utcDayKey(date);
    const row = byDay.get(key);
    out.push({
      airshipJobs: row?.airshipJobs ?? 0,
      day: date.toISOString(),
      failed: row?.failed ?? 0,
      jobs: row?.jobs ?? 0,
      otherJobs: (row?.jobs ?? 0) - (row?.airshipJobs ?? 0),
      queueP50Seconds: row?.queueP50Seconds ?? 0,
      runnerMinutes: row?.runnerMinutes ?? 0,
    });
  }
  return out;
}

function Report({
  days,
  report,
}: {
  days: number;
  report: Schemas["Analytics"];
}) {
  const series = fillDays(report.daily ?? [], days);
  const totals = series.reduce(
    (sum, day) => ({
      airshipJobs: sum.airshipJobs + day.airshipJobs,
      failed: sum.failed + day.failed,
      jobs: sum.jobs + day.jobs,
      runnerMinutes: sum.runnerMinutes + day.runnerMinutes,
    }),
    { airshipJobs: 0, failed: 0, jobs: 0, runnerMinutes: 0 }
  );
  if (totals.jobs === 0) {
    return (
      <EmptyRow>No jobs in the last {days} days. Push to see usage.</EmptyRow>
    );
  }

  return (
    <div className="grid gap-8">
      <section
        aria-label="Totals"
        className="grid grid-cols-2 gap-px bg-border ring-1 ring-border lg:grid-cols-4"
      >
        <Total
          label="Jobs on Airship"
          value={formatInteger(totals.airshipJobs)}
        />
        <Total label="Jobs seen" value={formatInteger(totals.jobs)} />
        <Total label="Failed" value={formatInteger(totals.failed)} />
        <Total
          label="Runner minutes"
          value={formatInteger(Math.round(totals.runnerMinutes))}
        />
      </section>
      <BarChart
        format={formatInteger}
        legend={[
          { key: "airshipJobs", label: "On Airship" },
          { key: "otherJobs", label: "Elsewhere" },
        ]}
        series={series}
        title="Jobs per day"
      />
      <BarChart
        format={(value) => `${formatInteger(Math.round(value))} min`}
        legend={[{ key: "runnerMinutes", label: "Runner minutes" }]}
        series={series}
        title="Runner minutes per day"
      />
      <BarChart
        format={formatDuration}
        legend={[{ key: "queueP50Seconds", label: "Median queue time" }]}
        series={series}
        title="Median wait from queued to started"
      />
      <section aria-labelledby="by-repository" className="grid gap-3">
        <h2 className="font-medium text-sm" id="by-repository">
          By repository
        </h2>
        <RepositoryTable rows={report.repositories ?? []} />
      </section>
    </div>
  );
}

function Total({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid gap-1 bg-card p-4">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="font-medium font-mono text-2xl tabular-nums tracking-tight">
        {value}
      </span>
    </div>
  );
}

type Measure =
  | "airshipJobs"
  | "otherJobs"
  | "runnerMinutes"
  | "queueP50Seconds";

interface Legend {
  key: Measure;
  label: string;
}

const WIDTH = 720;
const HEIGHT = 160;
const PAD_TOP = 8;
const PAD_BOTTOM = 20;
const GAP = 2;
const RADIUS = 2;
const TICKS = 3;
const dayFormat = { format: (date: Date) => formatUTCDay(date.toISOString()) };

// Two series at most: a saturated step and a light step of the same hue,
// stacked with a 2px surface gap so the split reads without color.
const fills = ["var(--chart-3)", "var(--chart-1)"];

/**
 * A stacked bar chart of one measure per day, drawn as inline SVG. The
 * tooltip follows the pointer; the table below carries the same numbers.
 */
function BarChart({
  format,
  legend,
  series,
  title,
}: {
  format: (value: number) => string;
  legend: Legend[];
  series: Day[];
  title: string;
}) {
  const id = useId();
  const [hovered, setHovered] = useState<number | null>(null);
  const total = (day: Day) => legend.reduce((sum, l) => sum + day[l.key], 0);
  const max = Math.max(1, ...series.map(total));
  const plotHeight = HEIGHT - PAD_TOP - PAD_BOTTOM;
  const slot = WIDTH / series.length;
  const barWidth = Math.max(2, slot - GAP);
  const y = (value: number) =>
    PAD_TOP + plotHeight - (value / max) * plotHeight;
  const active = hovered === null ? null : series[hovered];
  const labelEvery = Math.max(1, Math.ceil(series.length / 8));

  return (
    <figure aria-labelledby={`${id}-title`} className="grid gap-2">
      <figcaption className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 className="font-medium text-sm" id={`${id}-title`}>
          {title}
        </h2>
        {legend.length > 1 ? (
          <ul className="flex gap-4 text-muted-foreground text-xs">
            {legend.map((item, index) => (
              <li className="flex items-center gap-1.5" key={item.key}>
                <span
                  aria-hidden="true"
                  className="size-2"
                  style={{ background: fills[index] }}
                />
                {item.label}
              </li>
            ))}
          </ul>
        ) : null}
      </figcaption>
      <div
        className="relative"
        onPointerLeave={() => setHovered(null)}
        onPointerMove={(event) => setHovered(slotAt(event, series.length))}
      >
        <svg
          aria-describedby={`${id}-table`}
          className="h-auto w-full text-muted-foreground"
          role="img"
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        >
          <title>{title}</title>
          {Array.from({ length: TICKS }, (_, step) => {
            const value = (max * (step + 1)) / TICKS;
            return (
              <g key={value}>
                <line
                  stroke="currentColor"
                  strokeOpacity={0.15}
                  x1={0}
                  x2={WIDTH}
                  y1={y(value)}
                  y2={y(value)}
                />
                <text
                  className="font-mono"
                  fill="currentColor"
                  fontSize={9}
                  x={0}
                  y={y(value) - 2}
                >
                  {format(value)}
                </text>
              </g>
            );
          })}
          {series.map((day, index) => {
            const x = index * slot + GAP / 2;
            let stackTop = 0;
            return (
              <g
                key={day.day}
                opacity={hovered === null || hovered === index ? 1 : 0.55}
              >
                {legend.map((item, layer) => {
                  const value = day[item.key];
                  const bottom = stackTop;
                  stackTop += value;
                  if (value <= 0) {
                    return null;
                  }
                  const top = y(stackTop);
                  const height = y(bottom) - top - (layer > 0 ? GAP : 0);
                  return (
                    <rect
                      fill={fills[layer]}
                      height={Math.max(height, 1)}
                      key={item.key}
                      rx={layer === legend.length - 1 ? RADIUS : 0}
                      width={barWidth}
                      x={x}
                      y={top}
                    />
                  );
                })}
                {index % labelEvery === 0 ? (
                  <text
                    className="font-mono"
                    fill="currentColor"
                    fontSize={9}
                    textAnchor="middle"
                    x={x + barWidth / 2}
                    y={HEIGHT - 6}
                  >
                    {dayFormat.format(new Date(day.day))}
                  </text>
                ) : null}
              </g>
            );
          })}
        </svg>
        {active ? (
          <div
            className="pointer-events-none absolute top-0 grid gap-0.5 border bg-popover px-2 py-1.5 text-xs shadow-sm"
            style={{
              left: `${Math.min(((hovered ?? 0) + 0.5) / series.length, 0.8) * 100}%`,
            }}
          >
            <span className="font-medium">
              {dayFormat.format(new Date(active.day))}
            </span>
            {legend.map((item) => (
              <span className="flex justify-between gap-4" key={item.key}>
                <span className="text-muted-foreground">{item.label}</span>
                <span className="font-mono tabular-nums">
                  {format(active[item.key])}
                </span>
              </span>
            ))}
          </div>
        ) : null}
      </div>
      <details className="text-xs">
        <summary className="cursor-pointer text-muted-foreground">
          Show as a table
        </summary>
        <table className="mt-2 w-full" id={`${id}-table`}>
          <thead>
            <tr className="text-left text-muted-foreground">
              <th className="py-1 font-normal">Day</th>
              {legend.map((item) => (
                <th className="py-1 text-right font-normal" key={item.key}>
                  {item.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {series.map((day) => (
              <tr key={day.day}>
                <td className="py-0.5">
                  {dayFormat.format(new Date(day.day))}
                </td>
                {legend.map((item) => (
                  <td
                    className="py-0.5 text-right font-mono tabular-nums"
                    key={item.key}
                  >
                    {format(day[item.key])}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  );
}

/** Which day's slot the pointer is over, from its position in the plot. */
function slotAt(event: PointerEvent<HTMLDivElement>, slots: number): number {
  const box = event.currentTarget.getBoundingClientRect();
  const fraction = (event.clientX - box.left) / box.width;
  return Math.min(slots - 1, Math.max(0, Math.floor(fraction * slots)));
}

function RepositoryTable({ rows }: { rows: Schemas["RepositoryUsage"][] }) {
  if (rows.length === 0) {
    return <EmptyRow>No repositories ran jobs in this window.</EmptyRow>;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Repository</TableHead>
          <TableHead className="text-right">Jobs on Airship</TableHead>
          <TableHead className="text-right">Jobs seen</TableHead>
          <TableHead className="text-right">Runner minutes</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.repository}>
            <TableCell className="font-medium">{row.repository}</TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {formatInteger(row.airshipJobs)}
            </TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {formatInteger(row.jobs)}
            </TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {formatInteger(Math.round(row.runnerMinutes))}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
