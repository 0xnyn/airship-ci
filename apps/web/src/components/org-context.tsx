"use client";

import type { Schemas } from "@airship/client";
import { createContext, use } from "react";

export interface OrgContextValue {
  /** Whether the signed-in user can change settings. */
  isAdmin: boolean;
  org: Schemas["Organization"];
}

export const OrgContext = createContext<OrgContextValue | null>(null);

/** The organization the current page belongs to. */
export function useOrg(): OrgContextValue {
  const value = use(OrgContext);
  if (!value) {
    throw new Error("useOrg must be used inside the organization shell");
  }
  return value;
}
