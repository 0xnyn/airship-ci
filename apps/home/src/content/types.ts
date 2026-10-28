// Shapes of the content the pages pass into their components. Text fields
// may wrap code in backticks (`airship.yaml`); components render those as
// <code>.

export interface Link {
  href: string;
  label: string;
}

/** The pill colour beside a section label. */
export type Accent =
  | "white"
  | "yellow"
  | "gold"
  | "cyan"
  | "blue"
  | "green"
  | "red";

export interface Heading {
  accent: Accent;
  description?: string;
  label: string;
  title: string;
}

/** Where the light falls in a grain backdrop. */
export interface Backdrop {
  angle: number;
  focus: number;
  intensity?: number;
}

export interface CodeFile {
  code: string;
  file: string;
}

export type IconName =
  | "cloud"
  | "file"
  | "spark"
  | "layers"
  | "lock"
  | "network"
  | "one-job"
  | "key"
  | "server";
