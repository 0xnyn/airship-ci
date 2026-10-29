/** Where the user-facing documentation lives. */
export const DOCS_URL = "https://docs.airshipci.com";

/**
 * The documentation pages the dashboard links out to, keyed by the title each
 * page carries. The key is the link text, so a link always names its
 * destination and the two cannot drift apart.
 */
export const docsPages = {
  "Access control": "/organization/access-control",
  "Amazon Web Services": "/clouds/aws",
  "airship.yaml": "/configuration/overview",
  "Capacity and limits": "/organization/capacity",
  "Developer API": "/api-reference/introduction",
  "Google Cloud": "/clouds/google-cloud",
  "Idle runners": "/performance/idle-runners",
  "Microsoft Azure": "/clouds/azure",
  "Organization-wide runners": "/organization/runners",
  "Security and trust": "/security",
  Troubleshooting: "/troubleshooting",
} as const;

export type DocsPage = keyof typeof docsPages;
