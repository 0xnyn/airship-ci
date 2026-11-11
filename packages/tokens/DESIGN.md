# Airship design tokens

`src/tokens.ts` is the system. Every colour, typeface, radius, shadow and
curve the marketing site (`apps/home`) and the dashboard (`apps/dash`, via
`@airship/ui`) use is written there once; this file explains what each group
is for.

## How it flows

```
src/tokens.ts ─► scripts/generate.mjs ─┬─► src/generated/brand.css  ─► apps/home (layout.astro)
                                       └─► src/generated/theme.css  ─► packages/ui globals.css ─► apps/dash
                                       └─► favicon.svg (home, docs), icon.svg (dash), docs logo/*.svg
src/beam.ts (pure geometry) ──────────────► grain-field.astro (home) and grain-field.tsx (dash)
src/icon.ts + src/beam.ts ─► apps/home/og/render.mjs ─► favicon.ico, apple icons, install icons, social cards
```

- `pnpm --filter @airship/tokens generate` rewrites both CSS files. It runs as
  part of `pnpm generate`; `pnpm check:generated` fails when they are stale.
- `pnpm --filter @airship/tokens test` checks freshness and contrast: every
  text pairing the components produce must clear WCAG AA (4.5:1) and every
  status mark 3:1, with alpha flattened onto the surface it lands on. It also
  checks that `apps/docs/docs.json`'s `colors` are the tokens'.
- `pnpm --filter @airship/tokens contrast` prints every ratio.

Never edit `src/generated/`. A value that is not in `tokens.ts` is a
proposal to add one, not something to type into a class.

## The palette

The brand is the landing page's night: a near-black ground with a faint
green cast (`palette.ground`), white at fixed strengths for ink and
hairlines, and seven signal colours (`signal`).

The dashboard does not wear that night. It is a tool, so it is drawn from a
pure-grey ramp (`neutral`) in the manner of a developer console: in the dark,
a black panel beside a near-black sidebar, white hairlines at low strength,
`#ededed` ink and `#a1a1a1` for secondary text; in daylight, white panels on
a `#fafafa` sidebar with `#171717` ink. Colour is kept for state, and every
signal is deepened in light mode until its text passes AA. The two surfaces
still share the mark, the type, the radii and the curve.

## Signals mean the same thing everywhere

| Signal | Mark | Meaning |
| --- | --- | --- |
| blue (`active`) | `bg-active` | A runner running a job; the primary chart series |
| green (`success`) | `bg-success` | Idle, connected, healthy, succeeded |
| gold (`warning`) | `bg-warning` | Launching, queued, needs attention |
| red (`danger`) | `bg-danger` | Failed, rejected |
| cyan (`info`) | `bg-info` | Focus ring (dark), information |
| yellow (`highlight`) | `text-highlight` | Labels such as `airship-build` |

Each has three strengths: the bare name for a dot or bar, `-foreground` for
text, `-surface` for a tinted banner behind that text. On the landing page
each section wears one as its dot; the dashboard keeps its navigation
monochrome and spends colour only on state.

Clouds have their own colour (`cloud.*`, `bg-cloud-aws`) wherever one is
named. Syntax colours (`syntax.*`) are shared by the landing's code card and
the dashboard's code blocks.

## Type

Geist for prose and headings, Geist Mono for readings. The landing page sets
labels, buttons and body copy in mono. The dashboard sets its chrome
(buttons, table headers, tabs, labels, breadcrumbs, figures) in sans with
tabular figures, and keeps mono for identifiers a person might copy: runner
and repository names, labels, SHAs, ARNs, regions, code. Dashboard headings
are semibold with tight negative tracking.

## Radii

One rule: each level of nesting is one step tighter, so corners stay
concentric.

| Rung | Value | Used for |
| --- | --- | --- |
| `xl` | 12px | The dashboard's inset panel, dialogs |
| `lg` | 8px | Cards, panels, tables, menus |
| `md` | 6px | Controls: buttons, inputs, menu items; landing framed visuals |
| `sm` | 4px | Badges, checkboxes; landing buttons |
| `xs` | 2px | Keycaps, tooltip arrows |
| `full` | — | Dots, pills, avatars |

## Shell

The dashboard is an inset layout: the sidebar sits on the ground, the page
sits in a raised panel `shell.inset` (8px) in from the window, rounded at
`xl` with a hairline and an edge of light (`shadow.inset`). The panel scrolls,
the window does not. The sidebar opens with the organization switcher and a
"Find…" button (⌘K, the command menu); each section has a G-key shortcut
(G J for jobs), and ⌘B folds the sidebar to a rail of icons. Settings and
forms sit in a settings card (`SettingsCard` in `apps/dash`): a title, a
body, and a muted footer bar holding a note and the actions. Measurements are
in `shell` and exposed as `--sidebar-width`, `--sidebar-rail-width`,
`--shell-inset`, `--topbar-height`, `--content-width`.

## Backdrop

`beam` is the "dawn sky" behind every framed visual on the landing page and
behind the dashboard's sign-in: a diagonal beam of dithered light with a
warm-to-cool fringe. `beamGeometry()` in `src/beam.ts` lays it out; both the
Astro and React components only render what it returns. It is decoration and
never carries meaning. `bg-halftone` is the dotted texture behind the hero,
reused behind the dashboard's empty states.

## Icons and social cards

One icon everywhere: the mark in white on a tile of the ground (`icon`),
drawn by `iconSvg()` in `src/icon.ts`. It is a tile rather than a bare mark
so it reads on a light tab bar as well as a dark one. Variants change only
the shape: `square` for iOS, which rounds the corners itself, and `maskable`
for Android, which crops to a circle and so needs a smaller mark.

`pnpm generate` writes the SVGs into each app's static folder, since that is
where browsers, Next.js and Mintlify look for them. Anything that needs a
browser to draw (the `.ico`, the PNG icons, and the social cards) comes from
`apps/home/og/render.mjs` (`pnpm --filter @airship/home media`):

| Site | Icons | Social card |
| --- | --- | --- |
| Landing | `public/favicon.{svg,ico}`, `apple-touch-icon.png` | `public/og/og.{jpg,mp4}` |
| Dashboard | `src/app/{icon.svg,favicon.ico,apple-icon.png}`, `public/icon-*.png` (the manifest) | `src/app/opengraph-image.jpg` |
| Docs | `favicon.svg`, `logo/{dark,light}.svg` | Per-page thumbnails over `og/background.jpg` |

Every card is the same template (`apps/home/og/card.html`): the beam from
`beamGeometry()`, colours from `brand.css`, a variant's copy from the script.
Rerun it after changing the card, the mark or the palette.

## Motion

One curve (`motion.ease`, `ease-smooth` in Tailwind) at three speeds. Colour
and opacity take `fast`, layout takes `slow`. Everything respects
`prefers-reduced-motion`.
