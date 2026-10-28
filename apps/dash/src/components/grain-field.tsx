import { type BeamOptions, beamGeometry } from "@airship/tokens/beam";
import { beam } from "@airship/tokens/tokens";
import { cn } from "@airship/ui/lib/utils";

/**
 * The landing page's "dawn sky": a diagonal beam of grained light with a
 * warm-to-cool fringe. Drawn from the same numbers as the marketing site's
 * Astro component, so the two are one picture. Decorative.
 */
export function GrainField({
  className,
  id,
  ...options
}: BeamOptions & { className?: string; id: string }) {
  const g = beamGeometry(options);
  const { dither, layers } = beam;

  return (
    <svg
      aria-hidden="true"
      className={cn(
        "pointer-events-none absolute inset-0 size-full bg-black",
        className
      )}
      focusable="false"
      preserveAspectRatio="xMidYMid slice"
      viewBox={`0 0 ${g.width} ${g.height}`}
    >
      <defs>
        <filter
          colorInterpolationFilters="sRGB"
          height="100%"
          id={`${id}-dither`}
          width="100%"
          x="0"
          y="0"
        >
          <feTurbulence
            baseFrequency={g.grain}
            numOctaves={dither.octaves}
            result="noise"
            seed={dither.seed}
            type="fractalNoise"
          />
          <feColorMatrix
            in="noise"
            result="noiseAlpha"
            type="matrix"
            values={dither.matrix}
          />
          <feComposite
            in="SourceGraphic"
            in2="noiseAlpha"
            k1="0"
            k2="1"
            k3="1"
            k4={dither.threshold}
            operator="arithmetic"
            result="sum"
          />
          <feComponentTransfer in="sum" result="mask">
            <feFuncA tableValues="0 1" type="discrete" />
          </feComponentTransfer>
          <feFlood floodColor="#fff" result="white" />
          <feComposite in="white" in2="mask" operator="in" />
        </filter>
        <linearGradient
          gradientTransform={g.transform}
          gradientUnits="userSpaceOnUse"
          id={`${id}-beam`}
          x1={g.x1}
          x2={g.x2}
          y1="0"
          y2="0"
        >
          {g.across.map((stop) => (
            <stop
              key={stop.offset}
              offset={stop.offset}
              stopColor={stop.color}
              stopOpacity={stop.opacity}
            />
          ))}
        </linearGradient>
        <linearGradient id={`${id}-fade`} x1="0" x2="0" y1="0" y2="1">
          {g.down.map((stop) => (
            <stop
              key={stop.offset}
              offset={stop.offset}
              stopColor={stop.color}
              stopOpacity={stop.opacity}
            />
          ))}
        </linearGradient>
        <mask id={`${id}-fade-mask`} maskContentUnits="userSpaceOnUse">
          <rect fill={`url(#${id}-fade)`} height={g.height} width={g.width} />
        </mask>
        <linearGradient
          gradientTransform={g.transform}
          gradientUnits="userSpaceOnUse"
          id={`${id}-tint`}
          x1={g.x1}
          x2={g.x2}
          y1="0"
          y2="0"
        >
          {g.fringe.map((stop) => (
            <stop
              key={stop.offset}
              offset={stop.offset}
              stopColor={stop.color}
            />
          ))}
        </linearGradient>
      </defs>
      <rect fill="#000" height={g.height} width={g.width} />
      <rect
        fill={`url(#${id}-beam)`}
        height={g.height}
        mask={`url(#${id}-fade-mask)`}
        opacity={layers.glow}
        width={g.width}
      />
      <g filter={`url(#${id}-dither)`} opacity={layers.grain}>
        <rect
          fill={`url(#${id}-beam)`}
          height={g.height}
          mask={`url(#${id}-fade-mask)`}
          width={g.width}
        />
      </g>
      <rect
        fill={`url(#${id}-tint)`}
        height={g.height}
        style={{ mixBlendMode: "multiply" }}
        width={g.width}
      />
    </svg>
  );
}
