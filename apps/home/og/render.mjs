// Renders every raster image Airship's sites serve, from @airship/tokens:
//
//   - the social cards, from og/card.html: the landing page's still and
//     video, the dashboard's still, and the backdrop the docs draw each
//     page's thumbnail over;
//   - the icons, from the same tile the SVG favicons are (src/icon.ts):
//     favicon.ico, the Apple home-screen icon, and the dashboard's install
//     icons.
//
// The SVG icons and logos are not made here: `pnpm generate` writes those
// (packages/tokens/scripts/generate.mjs), since they need no browser.
//
//   pnpm --filter @airship/home media           everything
//   pnpm --filter @airship/home media icons     only the icons
//   pnpm --filter @airship/home media cards     only the cards
//
// Needs Chromium (`pnpm exec playwright install chromium`) and, for the
// landing page's video and the JPEGs, ffmpeg on PATH. JPEG and H.264 are the
// smallest formats link previews accept (a GIF of the same loop is four times
// the MP4).
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { beamGeometry } from "@airship/tokens/beam";
import { iconSvg } from "@airship/tokens/icon";
import { beam, brand } from "@airship/tokens/tokens";
import { chromium } from "playwright";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..", "..");
const at = (path) => join(root, path);

const WIDTH = 1200;
const HEIGHT = 630;
const SECONDS = 6;
const FPS = 30;
const STILL_AT = 2.2;

// The runner list on the cards: one row per kind of machine people run.
const ROWS = [
  { job: "test (node 24)", label: "airship-build", machine: "c7i.xlarge" },
  { job: "train-smoke", label: "airship-gpu", machine: "g2-standard-8" },
  { job: "e2e (shard 3/4)", label: "airship-build", machine: "m7i.xlarge" },
  { job: "msbuild release", label: "airship-windows", machine: "D4s_v5" },
  { job: "cargo build", label: "airship-arm", machine: "c7g.2xlarge" },
];

const CARDS = [
  {
    copy: {
      label: "Fully managed GitHub Actions runners",
      rows: ROWS,
      section: "Runners",
      sub: "Autoscaling runners in your AWS, GCP, or Azure account.",
      title: "Run your CI\nin your cloud.",
      url: "airshipci.com",
    },
    still: "apps/home/public/og/og.jpg",
    video: "apps/home/public/og/og.mp4",
  },
  {
    copy: {
      accent: "blue",
      label: "Dashboard",
      rows: ROWS,
      section: "Runners",
      sub: "Every runner, job and minute across your AWS, Google Cloud and Azure accounts.",
      title: "Your fleet,\nlive.",
      url: "app.airshipci.com",
    },
    still: "apps/dash/src/app/opengraph-image.jpg",
  },
  {
    // The docs' thumbnails write each page's own title over this.
    copy: { backdrop: true },
    still: "apps/docs/og/background.jpg",
  },
];

const ICON_SIZES = [16, 32, 48];
const ICONS = [
  { ico: "apps/home/public/favicon.ico" },
  { ico: "apps/dash/src/app/favicon.ico" },
  { png: "apps/home/public/apple-touch-icon.png", size: 180, square: true },
  { png: "apps/dash/src/app/apple-icon.png", size: 180, square: true },
  { png: "apps/dash/public/icon-192.png", size: 192 },
  { png: "apps/dash/public/icon-512.png", size: 512 },
  { maskable: true, png: "apps/dash/public/icon-maskable-512.png", size: 512 },
];

const run = (cmd, args) =>
  new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { stdio: ["ignore", "ignore", "inherit"] });
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? resolve() : reject(new Error(`${cmd} exited with ${code}`))
    );
  });

const wrote = [];
const write = async (path, data) => {
  await mkdir(dirname(at(path)), { recursive: true });
  await writeFile(at(path), data);
  wrote.push(path);
};

// An .ico is a directory of images; each entry here is a whole PNG, which
// every browser that still asks for /favicon.ico reads.
function ico(pngs) {
  const header = Buffer.alloc(6 + 16 * pngs.length);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(pngs.length, 4);
  let offset = header.length;
  for (const [i, { data, size }] of pngs.entries()) {
    const entry = 6 + 16 * i;
    header.writeUInt8(size % 256, entry);
    header.writeUInt8(size % 256, entry + 1);
    header.writeUInt16LE(1, entry + 4);
    header.writeUInt16LE(32, entry + 6);
    header.writeUInt32LE(data.length, entry + 8);
    header.writeUInt32LE(offset, entry + 12);
    offset += data.length;
  }
  return Buffer.concat([header, ...pngs.map(({ data }) => data)]);
}

async function renderIcons(chrome) {
  const page = await chrome.newPage({ deviceScaleFactor: 1 });
  const raster = async (size, options) => {
    await page.setViewportSize({ height: size, width: size });
    await page.setContent(
      `<style>html,body{margin:0;background:transparent}svg{display:block}</style>${iconSvg({ ...options, size })}`
    );
    return page.screenshot({ omitBackground: true, type: "png" });
  };

  for (const spec of ICONS) {
    if (spec.ico) {
      const pngs = [];
      for (const size of ICON_SIZES) {
        // biome-ignore lint/performance/noAwaitInLoops: one page, one image at a time
        pngs.push({ data: await raster(size), size });
      }
      await write(spec.ico, ico(pngs));
    } else {
      const { png, size, ...options } = spec;
      await write(png, await raster(size, options));
    }
  }
  await page.close();
}

async function renderCards(chrome, framesDir) {
  // The card's frame is wider than the landing page's visuals, so the beam
  // sits further right to clear the copy.
  const card = {
    beam: { ...beam, geometry: beamGeometry({ focus: 0.72 }) },
    brand,
  };

  for (const { copy, still, video } of CARDS) {
    // biome-ignore lint/performance/noAwaitInLoops: one card at a time
    const page = await chrome.newPage({
      deviceScaleFactor: 1,
      viewport: { height: HEIGHT, width: WIDTH },
    });
    await page.addInitScript(
      (value) => {
        window.CARD = value;
      },
      { ...card, copy }
    );
    await page.goto(pathToFileURL(join(here, "card.html")).href);
    await page.evaluate(() => document.fonts.ready);

    const shoot = async (t, path) => {
      await page.evaluate((time) => window.render(time), t);
      await page.screenshot({ path });
    };

    const png = join(framesDir, "still.png");
    await shoot(STILL_AT, png);
    await mkdir(dirname(at(still)), { recursive: true });
    await run("ffmpeg", [
      "-y",
      "-loglevel",
      "error",
      "-i",
      png,
      "-q:v",
      "3",
      at(still),
    ]);
    wrote.push(still);

    if (video) {
      const total = SECONDS * FPS;
      for (let i = 0; i < total; i += 1) {
        const name = `${String(i).padStart(4, "0")}.png`;
        // biome-ignore lint/performance/noAwaitInLoops: one page, one frame at a time
        await shoot(i / FPS, join(framesDir, name));
      }
      await run("ffmpeg", [
        "-y",
        "-loglevel",
        "error",
        "-framerate",
        String(FPS),
        "-i",
        join(framesDir, "%04d.png"),
        "-c:v",
        "libx264",
        "-preset",
        "slow",
        "-crf",
        "26",
        "-pix_fmt",
        "yuv420p",
        "-movflags",
        "+faststart",
        at(video),
      ]);
      wrote.push(video);
    }
    await page.close();
  }
}

const [, , only] = process.argv;
const frames = await mkdtemp(join(tmpdir(), "airship-media-"));
const browser = await chromium.launch();
try {
  if (only !== "cards") {
    await renderIcons(browser);
  }
  if (only !== "icons") {
    await renderCards(browser, frames);
  }
} finally {
  await browser.close();
  await rm(frames, { force: true, recursive: true });
}

for (const path of wrote) {
  console.log(`wrote ${relative(root, at(path))}`);
}
