import { cn } from "@airship/ui/lib/utils";

/**
 * Which pair of signal colours an organization's mark is drawn in, picked
 * from its login so every organization keeps the same mark everywhere.
 */
const gradients = [
  "from-active to-info",
  "from-info to-success",
  "from-success to-warning",
  "from-warning to-danger",
  "from-danger to-active",
] as const;

function gradientFor(login: string): string {
  let hash = 0;
  for (const char of login.toLowerCase()) {
    hash = (hash * 31 + char.charCodeAt(0)) % 2_147_483_647;
  }
  return gradients[hash % gradients.length] ?? gradients[0];
}

/** An organization's avatar: a gradient disc with its initial. */
export function OrgMark({
  className,
  login,
}: {
  className?: string;
  login: string;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "grid size-6 shrink-0 place-items-center rounded-full bg-linear-to-br font-semibold text-[11px] text-white uppercase",
        gradientFor(login),
        className
      )}
    >
      {login.slice(0, 1)}
    </span>
  );
}
