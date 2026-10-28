import { beam } from "./tokens.ts";

/** What a caller may change about the beam; everything else is fixed. */
export interface BeamOptions {
  /** Angle of the beam in degrees. */
  angle?: number;
  /** Where the beam's bright core sits, as a fraction of the width. */
  focus?: number;
  /** Grain size: higher is finer. */
  grain?: number;
  /** Overall brightness, 0 to 1. */
  intensity?: number;
}

/** A gradient stop, ready to render as `<stop offset stop-color stop-opacity>`. */
export interface BeamStop {
  color: string;
  offset: number;
  opacity: number;
}

/** Everything a renderer needs to draw one beam, in frame units. */
export interface BeamGeometry {
  /** Stops across the beam: white at the eased brightness. */
  across: BeamStop[];
  /** Stops down the frame: how the light fades towards the bottom. */
  down: BeamStop[];
  /** Stops of the colour fringe. */
  fringe: BeamStop[];
  grain: number;
  height: number;
  /** `gradientTransform` for the two gradients that follow the beam. */
  transform: string;
  width: number;
  x1: number;
  x2: number;
}

const WHITE = "#fff";

/**
 * Lays out the beam for one frame. Pure, so the landing page's Astro
 * component and the dashboard's React one draw the same picture.
 */
export function beamGeometry(options: BeamOptions = {}): BeamGeometry {
  const {
    angle = beam.defaults.angle,
    focus = beam.defaults.focus,
    grain = beam.defaults.grain,
    intensity = beam.defaults.intensity,
  } = options;
  const { width, height } = beam.viewBox;
  const cx = Math.round(focus * width);
  const at = (value: number) => Number((value * intensity).toFixed(3));

  return {
    across: beam.across.map(([offset, opacity]) => ({
      color: WHITE,
      offset,
      opacity: at(opacity),
    })),
    down: beam.down.map(([offset, opacity]) => ({
      color: WHITE,
      offset,
      opacity,
    })),
    fringe: beam.fringe.map(([offset, color]) => ({
      color,
      offset,
      opacity: 1,
    })),
    grain,
    height,
    transform: `rotate(${angle} ${cx} 0)`,
    width,
    x1: cx - beam.reach,
    x2: cx + beam.reach,
  };
}
