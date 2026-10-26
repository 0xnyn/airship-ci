interface FieldProblem {
  location?: string;
  message?: string;
}

const BODY_PREFIX = /^body\./;

/** The problem-details status of a failed query or mutation, if any. */
export function errorStatus(error: unknown): number | undefined {
  if (typeof error === "object" && error !== null && "status" in error) {
    const { status } = error;
    return typeof status === "number" ? status : undefined;
  }
  return undefined;
}

/**
 * A readable message for a failed query or mutation. Validation failures
 * name the first offending field, e.g. "maxRunners: expected number <= 1000".
 */
export function errorMessage(error: unknown, fallback: string): string {
  if (typeof error !== "object" || error === null) {
    return fallback;
  }
  if ("errors" in error && Array.isArray(error.errors)) {
    const [first] = error.errors as FieldProblem[];
    if (first?.message) {
      const field = first.location?.replace(BODY_PREFIX, "");
      return field ? `${field}: ${first.message}` : first.message;
    }
  }
  if ("detail" in error && typeof error.detail === "string") {
    return error.detail;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
}
