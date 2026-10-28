import { brand, font, icon } from "./tokens.ts";

/** The side of the box the mark is drawn in (`brand.markViewBox`). */
const MARK_BOX = 16;
/** The icon's own coordinate space. */
const TILE = 24;

const round = (value: number) => Number(value.toFixed(3));

/** Places the mark centred in the tile, spanning `scale` of it. */
function markTransform(scale: number): string {
  const [x = 0, y = 0] = brand.markViewBox.split(" ").map(Number);
  const k = (TILE * scale) / MARK_BOX;
  const centre = TILE / 2;
  const tx = round(centre - (x + MARK_BOX / 2) * k);
  const ty = round(centre - (y + MARK_BOX / 2) * k);
  return `translate(${tx} ${ty}) scale(${round(k)})`;
}

export interface IconOptions {
  /**
   * A maskable icon (a home-screen icon the platform crops to its own
   * shape): square corners and a smaller mark that survives the crop.
   */
  maskable?: boolean;
  /** The width and height the SVG declares. */
  size?: number;
  /**
   * Square corners, for a platform that rounds the icon itself (an iOS home
   * screen); a rounded tile there would leave dark corners showing.
   */
  square?: boolean;
}

/**
 * The app icon as an SVG document: the favicon, the dashboard's tab icon,
 * the docs' favicon, and the source every PNG and the .ico are rasterised
 * from.
 */
export function iconSvg({
  maskable = false,
  size = 32,
  square = false,
}: IconOptions = {}) {
  const corner = maskable || square ? 0 : round(TILE * icon.corner);
  const scale = maskable ? icon.maskableScale : icon.scale;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${TILE} ${TILE}" fill="none" role="img" aria-label="Airship">
  <rect width="${TILE}" height="${TILE}" rx="${corner}" fill="${icon.tile}"/>
  <path d="${brand.mark}" fill="${icon.ink}" fill-opacity="${icon.inkOpacity}" transform="${markTransform(scale)}"/>
</svg>
`;
}

/**
 * The wordmark (the mark and "airship") as an SVG document, for a page that
 * shows a logo image rather than drawing one in code: the docs' header.
 */
export function wordmarkSvg(scheme: "dark" | "light") {
  const ink = scheme === "dark" ? icon.ink : icon.wordmarkLight;
  const opacity = scheme === "dark" ? ` fill-opacity="${icon.inkOpacity}"` : "";
  return `<svg xmlns="http://www.w3.org/2000/svg" width="112" height="24" viewBox="0 0 112 24" fill="none" role="img" aria-label="Airship">
  <path d="${brand.mark}" fill="${ink}"${opacity}/>
  <text x="28" y="17" fill="${ink}"${opacity} font-family="${font.sans.replaceAll('"', "'")}" font-size="17" font-weight="500" letter-spacing="-0.6">${brand.name}</text>
</svg>
`;
}
