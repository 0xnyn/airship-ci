// Renders the social card in og/card.html to public/og/og.jpg and
// public/og/og.mp4. JPEG and H.264 are the smallest formats here that link
// previews accept (a GIF of the same loop is four times the MP4). Run with `pnpm --filter @airship/home og`; needs
// Chromium (`pnpm exec playwright install chromium`) and ffmpeg on PATH.
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { chromium } from "playwright";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "public", "og");
const WIDTH = 1200;
const HEIGHT = 630;
const SECONDS = 6;
const FPS = 30;
const STILL_AT = 2.2;

const run = (cmd, args) =>
  new Promise((resolve, reject) => {
    const child = spawn(cmd, args, { stdio: ["ignore", "ignore", "inherit"] });
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? resolve() : reject(new Error(`${cmd} exited with ${code}`))
    );
  });

await mkdir(out, { recursive: true });
const frames = await mkdtemp(join(tmpdir(), "airship-og-"));
const browser = await chromium.launch();

try {
  const page = await browser.newPage({
    deviceScaleFactor: 1,
    viewport: { height: HEIGHT, width: WIDTH },
  });
  await page.goto(pathToFileURL(join(here, "card.html")).href);
  await page.evaluate(() => document.fonts.ready);

  const shoot = async (t, path) => {
    await page.evaluate((time) => window.render(time), t);
    await page.screenshot({ path });
  };

  const still = join(frames, "still.png");
  await shoot(STILL_AT, still);
  await run("ffmpeg", [
    "-y",
    "-loglevel",
    "error",
    "-i",
    still,
    "-q:v",
    "4",
    join(out, "og.jpg"),
  ]);

  const total = SECONDS * FPS;
  for (let i = 0; i < total; i += 1) {
    const name = `${String(i).padStart(4, "0")}.png`;
    // biome-ignore lint/performance/noAwaitInLoops: one page, one frame at a time
    await shoot(i / FPS, join(frames, name));
  }

  await run("ffmpeg", [
    "-y",
    "-loglevel",
    "error",
    "-framerate",
    String(FPS),
    "-i",
    join(frames, "%04d.png"),
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
    join(out, "og.mp4"),
  ]);
} finally {
  await browser.close();
  await rm(frames, { force: true, recursive: true });
}

console.log(`Wrote ${join(out, "og.jpg")} and ${join(out, "og.mp4")}`);
