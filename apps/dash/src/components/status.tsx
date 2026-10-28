import type { Schemas } from "@airship/client";
import { cn } from "@airship/ui/lib/utils";

type Tone = "neutral" | "info" | "active" | "success" | "warning" | "danger";

// The landing page's runner states, in the same colours: busy is blue, idle
// is green, launching is gold. Values come from @airship/tokens.
const toneDot: Record<Tone, string> = {
  active: "bg-active",
  danger: "bg-danger",
  info: "bg-info",
  neutral: "bg-faint",
  success: "bg-success",
  warning: "bg-warning",
};

export function StatusDot({
  label,
  muted = false,
  pulse = false,
  tone,
}: {
  label: string;
  /** A settled, past state: the label steps back. */
  muted?: boolean;
  pulse?: boolean;
  tone: Tone;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap",
        muted && "text-muted-foreground"
      )}
    >
      <span aria-hidden="true" className="relative flex size-1.5 shrink-0">
        {pulse ? (
          <span
            className={cn(
              "absolute inset-0 animate-ping rounded-full opacity-60 [animation-duration:1.8s]",
              toneDot[tone]
            )}
          />
        ) : null}
        <span className={cn("relative size-1.5 rounded-full", toneDot[tone])} />
      </span>
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
    return <StatusDot label="Running" pulse tone="active" />;
  }
  const conclusion = job.conclusion ?? "completed";
  const tone = conclusionTone[conclusion] ?? "neutral";
  return (
    <StatusDot
      label={humanize(conclusion)}
      muted={tone === "neutral"}
      tone={tone}
    />
  );
}

const finishTone: Record<string, Tone> = {
  cancelled: "neutral",
  completed: "success",
  deleted: "neutral",
  idle: "neutral",
  launch_failed: "danger",
  launch_unconfirmed: "warning",
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
  launch_unconfirmed: "Launch unconfirmed",
  lease_expired: "Never started",
  lost: "Lost",
  max_lifetime: "Max lifetime",
};

export function RunnerState({ runner }: { runner: Schemas["Runner"] }) {
  switch (runner.state) {
    case "pending":
      return <StatusDot label="Waiting for capacity" pulse tone="neutral" />;
    case "launching":
      return <StatusDot label="Launching" pulse tone="warning" />;
    case "running":
      return <StatusDot label="Idle" tone="success" />;
    case "busy":
      return <StatusDot label="Running job" pulse tone="active" />;
    default: {
      const reason = runner.finishReason ?? "completed";
      const tone = finishTone[reason] ?? "neutral";
      return (
        <StatusDot
          label={finishLabel[reason] ?? humanize(reason)}
          muted={tone === "neutral" || tone === "success"}
          tone={tone}
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
  return (
    <StatusDot
      label={humanize(level)}
      muted={level === "info"}
      tone={levelTone[level]}
    />
  );
}

function humanize(value: string): string {
  const spaced = value.replaceAll("_", " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
