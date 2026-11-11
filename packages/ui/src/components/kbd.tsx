import { cn } from "@airship/ui/lib/utils";

/** A keycap: one key of a shortcut. */
function Kbd({ className, ...props }: React.ComponentProps<"kbd">) {
  return (
    <kbd
      className={cn(
        "pointer-events-none inline-flex h-5 min-w-5 select-none items-center justify-center rounded-xs border bg-muted px-1 font-medium font-sans text-[11px] text-muted-foreground leading-none",
        className
      )}
      data-slot="kbd"
      {...props}
    />
  );
}

/** Keycaps pressed together or in sequence. */
function KbdGroup({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      className={cn("inline-flex items-center gap-0.5", className)}
      data-slot="kbd-group"
      {...props}
    />
  );
}

export { Kbd, KbdGroup };
