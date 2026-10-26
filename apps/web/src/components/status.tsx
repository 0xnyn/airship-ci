import type { Schemas } from "@airship/client";
import { cn } from "@airship/ui/lib/utils";

type Tone = "neutral" | "info" | "success" | "warning" | "danger";

const toneDot: Record<Tone, string> = {
  danger: "bg-destructive",
  info: "bg-sky-500",
  neutral: "bg-muted-foreground/60",
  success: "bg-emerald-500",
  warning: "bg-amber-500",
};

export function StatusDot({
  label,
  pulse = false,
  tone,
}: {
  label: string;
  pulse?: boolean;
  tone: Tone;
}) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      <span
        aria-hidden="true"
        className={cn(
          "size-1.5 shrink-0",
          toneDot[tone],
          pulse && "animate-pulse"
        )}
      />
      {label}
    </span>
  );
}

const conclusionTone: Record<string, Tone> = {
  action_required: "warning",
  cancelled: "neutral",
  failure: "danger",
  neutral: "neutral",
  skipped: "neutral",
  stale: "neutral",
  success: "success",
  timed_out: "danger",
};

export function JobStatus({ job }: { job: Schemas["Job"] }) {
  if (job.status === "queued") {
    return <StatusDot label="Queued" pulse tone="warning" />;
  }
  if (job.status === "in_progress") {
    return <StatusDot label="Running" pulse tone="info" />;
  }
  const conclusion = job.conclusion ?? "completed";
  return (
    <StatusDot
      label={humanize(conclusion)}
      tone={conclusionTone[conclusion] ?? "neutral"}
    />
  );
}

const finishTone: Record<string, Tone> = {
  cancelled: "neutral",
  completed: "success",
  deleted: "neutral",
  idle: "neutral",
  launch_failed: "danger",
  lease_expired: "warning",
  lost: "danger",
  max_lifetime: "warning",
};

const finishLabel: Record<string, string> = {
  cancelled: "Not needed",
  completed: "Completed",
  deleted: "App uninstalled",
  idle: "Idle timeout",
  launch_failed: "Launch failed",
  lease_expired: "Never started",
  lost: "Lost",
  max_lifetime: "Max lifetime",
};

export function RunnerState({ runner }: { runner: Schemas["Runner"] }) {
  switch (runner.state) {
    case "pending":
      return <StatusDot label="Waiting for capacity" pulse tone="warning" />;
    case "launching":
      return <StatusDot label="Launching" pulse tone="info" />;
    case "running":
      return <StatusDot label="Idle" tone="info" />;
    case "busy":
      return <StatusDot label="Running job" pulse tone="success" />;
    default: {
      const reason = runner.finishReason ?? "completed";
      return (
        <StatusDot
          label={finishLabel[reason] ?? humanize(reason)}
          tone={finishTone[reason] ?? "neutral"}
        />
      );
    }
  }
}

export function CloudStatus({ cloud }: { cloud: Schemas["Cloud"] }) {
  if (cloud.status === "ok") {
    return <StatusDot label="Connected" tone="success" />;
  }
  return <StatusDot label="Credentials rejected" tone="danger" />;
}

const levelTone: Record<Schemas["Event"]["level"], Tone> = {
  error: "danger",
  info: "neutral",
  warning: "warning",
};

export function EventLevel({ level }: { level: Schemas["Event"]["level"] }) {
  return <StatusDot label={humanize(level)} tone={levelTone[level]} />;
}

function humanize(value: string): string {
  const spaced = value.replaceAll("_", " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
