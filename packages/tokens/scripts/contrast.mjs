// Fails when a colour pairing the components produce drops below WCAG AA.
//
// Alpha is flattened onto the surface it actually lands on before measuring,
// so `muted-foreground` (ink at 50%) is judged as the grey it renders as on
// each ground. A component that needs a pairing not listed here must add it
// here first.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { cloud, docs, palette, signal, syntax, theme } from "../src/tokens.ts";

const TEXT = 4.5;
const MARK = 3;

const parse = (hex) => {
  const h = hex.replace("#", "");
  const full = h.length <= 4 ? [...h].map((c) => c + c).join("") : h;
  const n = (i) => Number.parseInt(full.slice(i, i + 2), 16);
  return { a: full.length === 8 ? n(6) / 255 : 1, b: n(4), g: n(2), r: n(0) };
};

/** Paints `top` over an opaque `bottom`. */
const over = (top, bottom) => ({
  a: 1,
  b: top.b * top.a + bottom.b * (1 - top.a),
  g: top.g * top.a + bottom.g * (1 - top.a),
  r: top.r * top.a + bottom.r * (1 - top.a),
});

/** Resolves a stack of colours, bottom first, to one opaque colour. */
const flatten = (...layers) =>
  layers.map(parse).reduce((below, layer) => over(layer, below));

const luminance = ({ r, g, b }) => {
  const channel = (v) => {
    const c = v / 255;
    return c <= 0.039_28 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
};

const ratio = (a, b) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

const failures = [];
const rows = [];

/** `ground` is a stack, bottom first; `ink` lands on top of it. */
const expect = (label, ink, ground, min) => {
  const surface = flatten(...ground);
  const value = ratio(over(parse(ink), surface), surface);
  rows.push([label, value]);
  if (value < min) {
    failures.push(`${label}: ${value.toFixed(2)} < ${min}`);
  }
};

for (const [mode, t] of Object.entries(theme)) {
  const clouds = cloud[mode];
  const grounds = {
    accent: [t.background, t.accent],
    background: [t.background],
    card: [t.card],
    muted: [t.card, t.muted],
    popover: [t.popover],
    sidebar: [t.sidebar],
  };
  for (const [name, ground] of Object.entries(grounds)) {
    expect(`${mode} foreground on ${name}`, t.foreground, ground, TEXT);
    expect(
      `${mode} muted-foreground on ${name}`,
      t.mutedForeground,
      ground,
      TEXT
    );
  }
  for (const name of ["background", "card"]) {
    const ground = grounds[name];
    expect(
      `${mode} soft-foreground on ${name}`,
      t.softForeground,
      ground,
      TEXT
    );
    expect(`${mode} highlight on ${name}`, t.highlight, ground, TEXT);
    for (const s of ["success", "warning", "danger", "info", "active"]) {
      expect(
        `${mode} ${s}-foreground on ${name}`,
        t[`${s}Foreground`],
        ground,
        TEXT
      );
      expect(
        `${mode} ${s}-foreground on ${s}-surface over ${name}`,
        t[`${s}Foreground`],
        [...ground, t[`${s}Surface`]],
        TEXT
      );
      expect(`${mode} ${s} mark on ${name}`, t[s], ground, MARK);
    }
    for (const [c, value] of Object.entries(clouds)) {
      expect(`${mode} cloud-${c} on ${name}`, value, ground, TEXT);
      expect(`${mode} cloud-${c} on muted`, value, grounds.muted, TEXT);
    }
    expect(`${mode} ring on ${name}`, t.ring, ground, MARK);
    for (const [k, value] of Object.entries(syntax[mode])) {
      expect(`${mode} syntax-${k} on ${name}`, value, ground, TEXT);
    }
  }
  expect(
    `${mode} sidebar-foreground on sidebar`,
    t.sidebarForeground,
    grounds.sidebar,
    TEXT
  );
  expect(
    `${mode} sidebar-accent-foreground on sidebar-accent`,
    t.sidebarAccentForeground,
    [t.sidebar, t.sidebarAccent],
    TEXT
  );
  expect(
    `${mode} primary-foreground on primary`,
    t.primaryForeground,
    [t.primary],
    TEXT
  );
  expect(
    `${mode} secondary-foreground on secondary`,
    t.secondaryForeground,
    [t.background, t.secondary],
    TEXT
  );
  expect(
    `${mode} accent-foreground on accent`,
    t.accentForeground,
    grounds.accent,
    TEXT
  );
}

// The landing page's own pairings.
for (const ink of ["white", "white80", "white70", "white50"]) {
  for (const ground of ["ground", "panel", "well", "card"]) {
    expect(`brand ${ink} on ${ground}`, palette[ink], [palette[ground]], TEXT);
  }
}
expect("brand ink on white button", palette.ink, [palette.white], TEXT);
expect("brand white80 on control", palette.white80, [palette.control], TEXT);
for (const [name, value] of Object.entries(signal)) {
  expect(`brand dot-${name} on ground`, value, [palette.ground], MARK);
}

// The docs site's accents must be the tokens'.
const docsJson = JSON.parse(
  readFileSync(
    fileURLToPath(new URL("../../../apps/docs/docs.json", import.meta.url)),
    "utf8"
  )
);
for (const [key, value] of Object.entries(docs)) {
  if (docsJson.colors?.[key]?.toLowerCase() !== value.toLowerCase()) {
    failures.push(
      `apps/docs/docs.json colors.${key} is ${docsJson.colors?.[key]}, tokens say ${value}`
    );
  }
}

if (process.argv.includes("--report")) {
  for (const [label, value] of rows) {
    console.log(`${value.toFixed(2).padStart(6)}  ${label}`);
  }
}
if (failures.length > 0) {
  console.error(failures.join("\n"));
  process.exit(1);
}
console.log(`${rows.length} pairings pass AA.`);
