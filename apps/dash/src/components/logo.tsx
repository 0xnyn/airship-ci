import { brand } from "@airship/tokens/tokens";
import { cn } from "@airship/ui/lib/utils";

/** Airship's mark, and optionally its name, from the shared tokens. */
export function Logo({
  className,
  size = 18,
  wordmark = true,
}: {
  className?: string;
  size?: number;
  wordmark?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 text-foreground",
        className
      )}
    >
      <svg
        aria-hidden={wordmark ? true : undefined}
        aria-label={wordmark ? undefined : "Airship"}
        fill="none"
        height={size}
        role="img"
        viewBox={brand.markViewBox}
        width={size}
      >
        <path d={brand.mark} fill="currentColor" fillOpacity={0.92} />
      </svg>
      {wordmark ? (
        <span className="font-medium text-[15px] leading-none tracking-[-0.035em]">
          {brand.name}
        </span>
      ) : null}
    </span>
  );
}
