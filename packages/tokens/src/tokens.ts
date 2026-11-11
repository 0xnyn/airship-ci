/**
 * Airship's design tokens: the one place a colour, a typeface, a radius or a
 * duration is allowed to be written down.
 *
 * Two surfaces read from this file. The marketing site (`apps/home`) is dark
 * only and speaks in the palette below by its historical names (`--bg`,
 * `--white-80`, `--dot-blue`). The dashboard (`apps/dash`, through
 * `@airship/ui`) speaks in semantic roles (`background`, `muted-foreground`,
 * `success`) with a dark and a light value each, drawn from a pure-grey ramp
 * (`neutral`) so the product reads as a quiet tool beside the landing's
 * night. Both share the mark, the type, the radii and the curve.
 *
 * `scripts/generate.mjs` writes `src/generated/brand.css` and
 * `src/generated/theme.css` from this file; `--check` fails when either is
 * stale. `scripts/contrast.mjs` fails when a text pairing the components use
 * drops below WCAG AA. See DESIGN.md for how to use each group.
 */

/** The mark: an "A" split by a runway, drawn in a 16-unit box at (4, 4). */
export const brand = {
  mark: "M12 5.1L4 18.9H8.53L14.27 9.01ZM16.53 12.92H12L15.47 18.9H20Z",
  markViewBox: "4 4 16 16",
  name: "airship",
} as const;

/**
 * The night palette, as the landing page paints it. A near-black with a
 * faint green cast, white at fixed strengths for ink and hairlines, and a set
 * of signal colours that each section of the site wears as its dot.
 */
export const palette = {
  /** A card on the ground. */
  card: "#131517",
  /** A card one step up: a hovered row, a pressed control. */
  cardRaised: "#1a1c1f",
  /** A muted chip on a card (pricing's off state). */
  chip: "#464a50",
  /** The secondary button's fill. */
  control: "#232326",
  /** The page ground. Everything sits on this. */
  ground: "#080a09",
  /** The halftone dot texture behind the hero. */
  halftone: "#ffffff17",
  /** The near-black that sits on a white fill. */
  ink: "#08090a",
  /** The hairline between rows and around cards. */
  line: "#ffffff14",
  /** The hairline inside a table, one step fainter. */
  lineFaint: "#ffffff0d",
  /** A framed product panel on the ground (the hero's fleet panel). */
  panel: "#0a0b0d",
  /** A recessed well: a code card, the feature visual, the dashboard panel. */
  well: "#0c0d0f",
  white: "#ffffff",
  white30: "#ffffff4d",
  white50: "#ffffff80",
  white70: "#ffffffb3",
  white80: "#ffffffcc",
} as const;

/**
 * The app icon: the mark in white on a tile of the ground. Every surface
 * uses this one tile (a browser tab, a home screen, the docs), so it reads on
 * a light tab bar and a dark one alike. `src/icon.ts` draws it.
 */
export const icon = {
  /** The tile's corner radius, as a fraction of its side. */
  corner: 0.22,
  ink: palette.white,
  inkOpacity: 0.92,
  /**
   * How much of a maskable icon the mark spans. Android crops those to a
   * circle 80% across, so the mark stays well inside it.
   */
  maskableScale: 0.46,
  /** How much of the tile the mark spans. */
  scale: 0.62,
  tile: palette.ground,
  /** The wordmark's ink on a light page (the docs' light logo). */
  wordmarkLight: palette.ground,
} as const;

/**
 * Signal colours. On the landing page each section wears one as its dot; in
 * the dashboard they carry state. A runner's colour is the same in both: busy
 * is blue, idle is green, launching is gold.
 */
export const signal = {
  /** A runner running a job; the primary chart series. */
  blue: "#3064ff",
  /** Focus, links, information. */
  cyan: "#9efaff",
  /** Launching, warnings, the active feature marker. */
  gold: "#d4b144",
  /** Healthy, idle, connected, spot. */
  green: "#91cfb5",
  /** Failures. */
  red: "#ff6e5a",
  /** Section dot for the hero; the neutral state. */
  white: "#ffffff",
  /** Labels (`airship-build`) and the hero's sub line. */
  yellow: "#f3ffc9",
} as const;

/** The colour each cloud's name is set in, wherever a cloud is named. */
export const cloud = {
  dark: {
    aws: "#ffb36b",
    azure: "#6fd3ff",
    daytona: "#c7b3ff",
    gcp: "#8ab4ff",
  },
  light: {
    aws: "#a35200",
    azure: "#08709c",
    daytona: "#6446c2",
    gcp: "#2a5bd7",
  },
} as const;

/**
 * YAML and shell highlighting, as the landing page's code card sets it and
 * the dashboard's code blocks repeat it.
 */
export const syntax = {
  dark: {
    comment: palette.white50,
    expression: "#c7b3ff",
    key: signal.cyan,
    literal: "#ffb36b",
    string: signal.yellow,
  },
  light: {
    comment: "#08090a99",
    expression: "#6446c2",
    key: "#07707d",
    literal: "#a35200",
    string: "#5f6b12",
  },
} as const;

/** The docs site's accent trio (Mintlify `colors`), checked against this. */
export const docs = {
  dark: "#1f4ed8",
  light: signal.cyan,
  primary: signal.blue,
} as const;

/**
 * A semantic theme: what every dashboard component names. `sidebar` is the
 * ground the navigation sits on; `background` is the inset panel the page
 * content sits in, framed by a hairline; `card` is a framed surface on it.
 *
 * Signals come in three strengths: the bare name is the mark (a dot, a bar,
 * an icon), `-foreground` is the same hue tuned to pass AA as text on every
 * surface, and `-surface` is a tint for a banner or a badge behind that text.
 */
interface Theme {
  accent: string;
  accentForeground: string;
  active: string;
  activeForeground: string;
  activeSurface: string;
  background: string;
  border: string;
  card: string;
  cardForeground: string;
  chart1: string;
  chart2: string;
  chart3: string;
  chart4: string;
  chart5: string;
  danger: string;
  dangerForeground: string;
  dangerSurface: string;
  faint: string;
  foreground: string;
  halftone: string;
  highlight: string;
  info: string;
  infoForeground: string;
  infoSurface: string;
  input: string;
  muted: string;
  mutedForeground: string;
  popover: string;
  popoverForeground: string;
  primary: string;
  primaryForeground: string;
  ring: string;
  secondary: string;
  secondaryForeground: string;
  sidebar: string;
  sidebarAccent: string;
  sidebarAccentForeground: string;
  sidebarBorder: string;
  sidebarForeground: string;
  sidebarPrimary: string;
  sidebarPrimaryForeground: string;
  sidebarRing: string;
  softForeground: string;
  success: string;
  successForeground: string;
  successSurface: string;
  warning: string;
  warningForeground: string;
  warningSurface: string;
}

/**
 * The dashboard's neutral greys. The dashboard is a tool, not a poster: it
 * reads best in true black and white with grey ink, so its chrome is drawn
 * from this ramp rather than the landing's green-cast night, and colour is
 * kept for state alone. The ramp follows the shape of a pure-grey 1000-step
 * scale: `black` is the darkest ground, `n1000` the brightest ink.
 */
const neutral = {
  black: "#000000",
  ink: "#171717",
  ink600: "#4d4d4d",
  ink700: "#666666",
  n100: "#0a0a0a",
  n150: "#111111",
  n200: "#1a1a1a",
  n300: "#1f1f1f",
  n400: "#2e2e2e",
  n700: "#8f8f8f",
  n800: "#a1a1a1",
  n900: "#d4d4d4",
  n1000: "#ededed",
  paper: "#fafafa",
  paper200: "#f2f2f2",
  paper300: "#ebebeb",
  white: "#ffffff",
} as const;

/**
 * The dashboard in the dark: a black canvas, a near-black sidebar beside it,
 * hairlines of white at low strength and grey ink. Signals are clear,
 * saturated hues so state is the only colour on the page.
 */
const dark: Theme = {
  accent: neutral.n300,
  accentForeground: neutral.n1000,
  active: "#0090ff",
  activeForeground: "#52a8ff",
  activeSurface: "#0090ff1f",
  background: neutral.black,
  border: "#ffffff1a",
  card: neutral.n100,
  cardForeground: neutral.n1000,
  chart1: "#0090ff",
  chart2: "#ffffff2e",
  chart3: "#12a594",
  chart4: "#30a46c",
  chart5: "#ffb224",
  danger: "#e5484d",
  dangerForeground: "#ff6369",
  dangerSurface: "#e5484d1f",
  faint: "#ffffff4d",
  foreground: neutral.n1000,
  halftone: "#ffffff12",
  highlight: neutral.n900,
  info: "#12a594",
  infoForeground: "#0bd8b6",
  infoSurface: "#12a5941f",
  input: "#ffffff24",
  muted: neutral.n200,
  mutedForeground: neutral.n800,
  popover: neutral.n100,
  popoverForeground: neutral.n1000,
  primary: neutral.n1000,
  primaryForeground: neutral.n100,
  ring: "#0090ff",
  secondary: neutral.n200,
  secondaryForeground: neutral.n1000,
  sidebar: neutral.n100,
  sidebarAccent: "#ffffff14",
  sidebarAccentForeground: neutral.n1000,
  sidebarBorder: "#ffffff14",
  sidebarForeground: neutral.n800,
  sidebarPrimary: neutral.n1000,
  sidebarPrimaryForeground: neutral.n100,
  sidebarRing: "#0090ff",
  softForeground: neutral.n900,
  success: "#30a46c",
  successForeground: "#3dd68c",
  successSurface: "#30a46c1f",
  warning: "#ffb224",
  warningForeground: "#f1a10d",
  warningSurface: "#ffb2241a",
};

/**
 * The dashboard in daylight: white panels on a paper sidebar, near-black
 * ink, grey hairlines. Signals are deepened until their text passes AA on
 * white.
 */
const light: Theme = {
  accent: neutral.paper200,
  accentForeground: neutral.ink,
  active: "#0070f3",
  activeForeground: "#005ccc",
  activeSurface: "#0070f314",
  background: neutral.white,
  border: neutral.paper300,
  card: neutral.white,
  cardForeground: neutral.ink,
  chart1: "#0070f3",
  chart2: "#0000001f",
  chart3: "#0d9488",
  chart4: "#28a745",
  chart5: "#d97706",
  danger: "#e5484d",
  dangerForeground: "#c92a2f",
  dangerSurface: "#e5484d14",
  faint: "#00000040",
  foreground: neutral.ink,
  halftone: "#00000012",
  highlight: neutral.ink600,
  info: "#0d9488",
  infoForeground: "#0b7268",
  infoSurface: "#0d948814",
  input: "#e0e0e0",
  muted: neutral.paper,
  mutedForeground: neutral.ink700,
  popover: neutral.white,
  popoverForeground: neutral.ink,
  primary: neutral.ink,
  primaryForeground: neutral.white,
  ring: "#0070f3",
  secondary: neutral.paper200,
  secondaryForeground: neutral.ink,
  sidebar: neutral.paper,
  sidebarAccent: "#0000000f",
  sidebarAccentForeground: neutral.ink,
  sidebarBorder: neutral.paper300,
  sidebarForeground: neutral.ink700,
  sidebarPrimary: neutral.ink,
  sidebarPrimaryForeground: neutral.white,
  sidebarRing: "#0070f3",
  softForeground: neutral.ink600,
  success: "#28a745",
  successForeground: "#16742f",
  successSurface: "#28a74514",
  warning: "#d97706",
  warningForeground: "#a35200",
  warningSurface: "#d977061a",
};

export const theme = { dark, light } as const;

/**
 * Type. Geist for prose and headings, Geist Mono for anything read as a
 * measurement or an identifier: a label, a count, a duration, a runner name.
 * The landing page goes further and sets its body copy in mono; the dashboard
 * keeps long prose in sans and puts the mono on the readings.
 */
export const font = {
  mono: '"Geist Mono", ui-monospace, SFMono-Regular, monospace',
  sans: '"Geist", ui-sans-serif, system-ui, sans-serif',
  tracking: {
    /** Display headings. */
    display: "-0.04em",
    /** Mono labels at 11 to 12px. */
    label: "-0.02em",
    /** Section and card titles. */
    tight: "-0.025em",
  },
  weight: { medium: "500", regular: "400" },
} as const;

/**
 * Radii, a ladder with one rule: each level of nesting is one step tighter,
 * so concentric corners stay concentric. The inset panel at `xl` holds a card
 * at `lg` holds a control at `md` holds a badge at `sm`.
 *
 * The landing page draws its controls at `sm` (4px) and its framed visuals at
 * `md` (6px); the dashboard reuses those exactly and adds the two outer rungs
 * for its shell.
 */
export const radius = {
  "2xl": "16px",
  full: "9999px",
  lg: "8px",
  md: "6px",
  sm: "4px",
  xl: "12px",
  xs: "2px",
} as const;

/** Elevation. Dark surfaces separate by edge light, not by drop shadow. */
export const shadow = {
  /** A framed visual floating on the ground (landing code card, feature panel). */
  float: "0 24px 60px rgba(0, 0, 0, 0.55)",
  /** The hero window. */
  hero: "0 0 0 1px rgba(255, 255, 255, 0.06), 0 40px 120px rgba(0, 0, 0, 0.8)",
  /** The inset panel's top edge catching light, and a soft drop under it. */
  inset:
    "inset 0 1px 0 0 rgba(255, 255, 255, 0.04), 0 1px 2px rgba(0, 0, 0, 0.24)",
  /** A popover or menu. */
  overlay: "0 12px 32px rgba(0, 0, 0, 0.35)",
} as const;

/** One curve, three speeds. Colour and opacity take `fast`; size takes `slow`. */
export const motion = {
  base: "200ms",
  ease: "cubic-bezier(0.16, 1, 0.3, 1)",
  fast: "150ms",
  slow: "320ms",
} as const;

/** The dashboard shell's fixed measurements, in px. */
export const shell = {
  /** The widest a page's content column grows. */
  contentWidth: 1152,
  /** The gap between the window edge and the inset panel. */
  inset: 8,
  /** The collapsed sidebar: a rail of icons. */
  sidebarRailWidth: 56,
  sidebarWidth: 240,
  topBarHeight: 48,
} as const;

/**
 * The "dawn sky": the diagonal beam of grained light behind every framed
 * visual on the landing page, and behind the dashboard's sign-in. Both
 * renderers (an Astro component and a React one) draw it from these numbers.
 */
export const beam = {
  /** Brightness across the beam, as [offset, opacity], eased so it never steps. */
  across: [
    [0, 0.14],
    [0.2, 0.18],
    [0.32, 0.26],
    [0.42, 0.42],
    [0.5, 0.62],
    [0.58, 0.88],
    [0.66, 1],
    [0.78, 0.8],
    [0.9, 0.55],
    [1, 0.4],
  ],
  defaults: { angle: -24, focus: 0.62, grain: 1.35, intensity: 1 },
  /**
   * The static. Fractal noise becomes an alpha mask (`matrix`), the beam's
   * brightness is added to it and `threshold` decides which pixels light, so
   * a brighter region is a denser dither rather than a lighter grey.
   */
  dither: {
    matrix: "0 0 0 0 0  0 0 0 0 0  0 0 0 0 0  2.6 0 0 0 -0.8",
    octaves: 2,
    /** The noise seed, so every render is the same picture. */
    seed: 7,
    threshold: -0.52,
  },
  /** How the light falls off from the top of the frame to the bottom. */
  down: [
    [0, 1],
    [0.2, 0.92],
    [0.45, 0.7],
    [0.7, 0.48],
    [1, 0.32],
  ],
  /** The warm-to-cool fringe multiplied over the grain, as [offset, colour]. */
  fringe: [
    [0, "#5d6168"],
    [0.36, "#8a9097"],
    [0.42, "#ff6a3d"],
    [0.46, "#ff3d7f"],
    [0.5, "#7a5cff"],
    [0.55, "#5ad8ff"],
    [0.64, "#ffffff"],
    [1, "#d8dde3"],
  ],
  /** Opacity of the smooth glow and of the grain over it. */
  layers: { glow: 0.6, grain: 0.45 },
  /** Half the beam gradient's length along its axis, in frame units. */
  reach: 700,
  /** The frame's own coordinate box. */
  viewBox: { height: 700, width: 1000 },
} as const satisfies {
  across: readonly (readonly [number, number])[];
  down: readonly (readonly [number, number])[];
  fringe: readonly (readonly [number, string])[];
  [key: string]: unknown;
};

/** The halftone texture: a 1px dot on a 6px lattice. */
export const halftone = {
  dot: "1px",
  pitch: "6px",
} as const;
