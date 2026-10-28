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
import { PageHeader, SectionHeading } from "./page-header";
import { Panel, Stat, StatGrid } from "./panel";

type Window = "7" | "30" | "90";

const windows = [
  { label: "7 days", value: "7" },
  { label: "30 days", value: "30" },
  { label: "90 days", value: "90" },
] as const satisfies readonly { label: string; value: Window }[];

const DAY_MS = 24 * 60 * 60 * 1000;
const PERCENT = 100;
const MINUTES_PER_HOUR = 60;

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
      {analytics.isPending ? (
        <div className="grid gap-6">
          <Skeleton className="h-24 rounded-lg" />
          <Skeleton className="h-64 rounded-lg" />
        </div>
      ) : null}
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
    <div className="grid gap-6">
      <StatGrid label="Totals">
        <Stat
          dot="bg-active"
          hint={`${Math.round((totals.airshipJobs / totals.jobs) * PERCENT)}% of every job seen`}
          label="Jobs on Airship"
          value={formatInteger(totals.airshipJobs)}
        />
        <Stat
          hint={`Across ${formatInteger(report.repositories?.length ?? 0)} repositories`}
          label="Jobs seen"
          value={formatInteger(totals.jobs)}
        />
        <Stat
          dot={totals.failed > 0 ? "bg-danger" : "bg-success"}
          hint={`${((totals.failed / Math.max(1, totals.airshipJobs)) * PERCENT).toFixed(1)}% of jobs on Airship`}
          label="Failed"
          value={formatInteger(totals.failed)}
        />
        <Stat
          hint={`About ${formatInteger(Math.round(totals.runnerMinutes / MINUTES_PER_HOUR))} machine hours`}
          label="Runner minutes"
          value={formatInteger(Math.round(totals.runnerMinutes))}
        />
      </StatGrid>
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
        aggregate="average"
        format={formatDuration}
        legend={[{ key: "queueP50Seconds", label: "Median queue time" }]}
        series={series}
        title="Median wait from queued to started"
      />
      <section aria-labelledby="by-repository" className="mt-2 grid gap-3">
        <SectionHeading id="by-repository">By repository</SectionHeading>
        <RepositoryTable rows={report.repositories ?? []} />
      </section>
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
const HEIGHT = 176;
const GAP_RATIO = 0.28;
const TICKS = 3;
const LABELS = 6;
const dayFormat = { format: (date: Date) => formatUTCDay(date.toISOString()) };
const NICE_STEPS = [1, 2, 2.5, 5, 10];
const BASE = 10;

/** A round axis top: the smallest 1, 2, 2.5 or 5 × 10ⁿ step that fits. */
function niceMax(value: number): number {
  const raw = value / TICKS;
  const magnitude = BASE ** Math.floor(Math.log10(raw));
  const step =
    (NICE_STEPS.find((candidate) => candidate * magnitude >= raw) ?? BASE) *
    magnitude;
  return step * TICKS;
}

// Two series at most: the brand blue for what ran on Airship, a quiet ink
// for everything else, stacked so the split reads without a legend.
const fills = ["var(--chart-1)", "var(--chart-2)"];

/**
 * A stacked bar chart of one measure per day, drawn as inline SVG inside a
 * panel. Axis labels are HTML so they stay crisp at any width; the tooltip
 * follows the pointer and the table below carries the same numbers.
 */
function BarChart({
  aggregate = "sum",
  format,
  legend,
  series,
  title,
}: {
  /** How the header sums the window: a total, or the mean of active days. */
  aggregate?: "sum" | "average";
  format: (value: number) => string;
  legend: Legend[];
  series: Day[];
  title: string;
}) {
  const id = useId();
  const [hovered, setHovered] = useState<number | null>(null);
  const total = (day: Day) => legend.reduce((acc, l) => acc + day[l.key], 0);
  const max = niceMax(Math.max(1, ...series.map(total)));
  const slot = WIDTH / series.length;
  const barWidth = Math.max(1, slot * (1 - GAP_RATIO));
  const y = (value: number) => HEIGHT - (value / max) * HEIGHT;
  const active = hovered === null ? null : series[hovered];
  const labelEvery = Math.max(1, Math.ceil(series.length / LABELS));
  const sum = series.reduce((acc, day) => acc + total(day), 0);
  const activeDays = series.filter((day) => total(day) > 0).length;
  const overall = aggregate === "sum" ? sum : sum / Math.max(1, activeDays);
  const reading = active ? total(active) : overall;

  return (
    <Panel>
      <figure aria-labelledby={`${id}-title`} className="grid">
        <figcaption className="flex flex-wrap items-start justify-between gap-3 border-b px-4 py-3 md:px-5">
          <div className="grid gap-1">
            <h2
              className="font-medium text-[15px] tracking-tight"
              id={`${id}-title`}
            >
              {title}
            </h2>
            <p className="figure font-mono text-[11px] text-muted-foreground">
              {active ? dayFormat.format(new Date(active.day)) : null}
              {active || aggregate === "average" ? null : "In this window"}
              {!active && aggregate === "average" ? "Daily average" : null}
              {" · "}
              <span className="text-foreground">{format(reading)}</span>
            </p>
          </div>
          {legend.length > 1 ? (
            <ul className="flex gap-4 font-mono text-[11px] text-muted-foreground">
              {legend.map((item, index) => (
                <li className="flex items-center gap-1.5" key={item.key}>
                  <span
                    aria-hidden="true"
                    className="h-2 w-3.5 rounded-full"
                    style={{ background: fills[index] }}
                  />
                  {item.label}
                </li>
              ))}
            </ul>
          ) : null}
        </figcaption>
        <div className="px-4 pt-5 pb-3 md:px-5">
          <div
            className="relative ml-16"
            onPointerLeave={() => setHovered(null)}
            onPointerMove={(event) => setHovered(slotAt(event, series.length))}
          >
            {Array.from({ length: TICKS }, (_, step) => {
              const value = (max * (step + 1)) / TICKS;
              return (
                <span
                  aria-hidden="true"
                  className="figure pointer-events-none absolute -left-16 w-14 -translate-y-1/2 whitespace-nowrap text-right font-mono text-[10px] text-muted-foreground"
                  key={value}
                  style={{ top: `${(y(value) / HEIGHT) * PERCENT}%` }}
                >
                  {format(value)}
                </span>
              );
            })}
            <svg
              aria-describedby={`${id}-table`}
              className="block h-44 w-full overflow-visible"
              preserveAspectRatio="none"
              role="img"
              viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
            >
              <title>{title}</title>
              {Array.from({ length: TICKS }, (_, step) => {
                const value = (max * (step + 1)) / TICKS;
                return (
                  <line
                    key={value}
                    stroke="var(--border)"
                    strokeDasharray="2 3"
                    vectorEffect="non-scaling-stroke"
                    x1={0}
                    x2={WIDTH}
                    y1={y(value)}
                    y2={y(value)}
                  />
                );
              })}
              <line
                stroke="var(--border)"
                vectorEffect="non-scaling-stroke"
                x1={0}
                x2={WIDTH}
                y1={HEIGHT}
                y2={HEIGHT}
              />
              {series.map((day, index) => {
                const x = index * slot + (slot - barWidth) / 2;
                let stackTop = 0;
                return (
                  <g
                    className="transition-opacity duration-150"
                    key={day.day}
                    opacity={hovered === null || hovered === index ? 1 : 0.35}
                  >
                    {hovered === index ? (
                      <rect
                        fill="var(--muted)"
                        height={HEIGHT}
                        width={slot}
                        x={index * slot}
                        y={0}
                      />
                    ) : null}
                    {legend.map((item, layer) => {
                      const value = day[item.key];
                      const bottom = stackTop;
                      stackTop += value;
                      if (value <= 0) {
                        return null;
                      }
                      const top = y(stackTop);
                      return (
                        <rect
                          fill={fills[layer]}
                          height={Math.max(y(bottom) - top, 1)}
                          key={item.key}
                          width={barWidth}
                          x={x}
                          y={top}
                        />
                      );
                    })}
                  </g>
                );
              })}
            </svg>
            {active ? (
              <div
                className="pointer-events-none absolute top-2 z-10 grid min-w-36 gap-1 rounded-md border bg-popover px-2.5 py-2 text-xs shadow-overlay"
                style={{
                  left: `${Math.min(((hovered ?? 0) + 0.5) / series.length, 0.78) * PERCENT}%`,
                }}
              >
                <span className="font-medium">
                  {dayFormat.format(new Date(active.day))}
                </span>
                {legend.map((item, index) => (
                  <span
                    className="flex items-center justify-between gap-4"
                    key={item.key}
                  >
                    <span className="flex items-center gap-1.5 text-muted-foreground">
                      <span
                        aria-hidden="true"
                        className="size-1.5 rounded-full"
                        style={{ background: fills[index] }}
                      />
                      {item.label}
                    </span>
                    <span className="figure font-mono">
                      {format(active[item.key])}
                    </span>
                  </span>
                ))}
              </div>
            ) : null}
          </div>
          <div aria-hidden="true" className="relative mt-2 ml-16 h-4">
            {series.map((day, index) =>
              index % labelEvery === 0 ? (
                <span
                  className="absolute -translate-x-1/2 whitespace-nowrap font-mono text-[10px] text-muted-foreground first:translate-x-0"
                  key={day.day}
                  style={{
                    left: `${((index + 0.5) / series.length) * PERCENT}%`,
                  }}
                >
                  {dayFormat.format(new Date(day.day))}
                </span>
              ) : null
            )}
          </div>
        </div>
        <details className="border-t text-xs">
          <summary className="cursor-pointer px-4 py-2.5 font-mono text-[11px] text-muted-foreground transition-colors hover:text-foreground md:px-5">
            Show as a table
          </summary>
          <table className="w-full" id={`${id}-table`}>
            <thead>
              <tr className="border-y bg-muted/30 text-left font-mono text-[11px] text-muted-foreground">
                <th className="px-4 py-2 font-normal md:px-5">Day</th>
                {legend.map((item) => (
                  <th
                    className="px-4 py-2 text-right font-normal md:px-5"
                    key={item.key}
                  >
                    {item.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {series.map((day) => (
                <tr
                  className="border-border/70 border-b last:border-0"
                  key={day.day}
                >
                  <td className="px-4 py-1.5 md:px-5">
                    {dayFormat.format(new Date(day.day))}
                  </td>
                  {legend.map((item) => (
                    <td
                      className="figure px-4 py-1.5 text-right font-mono md:px-5"
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
    </Panel>
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
            <TableCell className="font-medium font-mono text-[12px]">
              {row.repository}
            </TableCell>
            <TableCell className="figure text-right font-mono">
              {formatInteger(row.airshipJobs)}
            </TableCell>
            <TableCell className="figure text-right font-mono">
              {formatInteger(row.jobs)}
            </TableCell>
            <TableCell className="figure text-right font-mono">
              {formatInteger(Math.round(row.runnerMinutes))}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
