// Builds the developer API's OpenAPI document from the control plane's own.
//
// apps/api/openapi.json describes every endpoint the Go API serves, including
// the dashboard's private surface. The documented API is the token-authenticated
// subset under /api, so this script keeps those paths, prunes the schemas
// nothing reachable references any more, and writes the result next to the
// reference pages for Mintlify to render.
//
// Run by `pnpm generate`; CI fails if the committed file is stale.

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const SOURCE = join(here, "..", "..", "api", "openapi.json");
const TARGET = join(here, "..", "api-reference", "openapi.json");

/** Paths the documented API offers. Everything else is the dashboard's own. */
const PUBLIC_PATHS = [
  "/api/v1/repo",
  "/api/v1/cloud-connect",
  "/api/v1/access-control",
  "/api/runner-provision",
];

/** Clouds the documentation covers, in the order the pages introduce them. */
const CLOUDS = ["aws", "gcp", "azure"];

/**
 * Prose the Go handlers write for their own audience, replaced with prose
 * written for an API consumer. Every value the source document carries must
 * appear here, so a reworded summary upstream fails the build instead of
 * reaching the published site unreviewed.
 */
const PROSE = {
  "Activate or deactivate a repository": "Activate or deactivate a repository",
  "Connect a cloud": "Connect a cloud",
  "Fetch a runner's boot script (provision_via: pull_from_api)":
    "Fetch a runner's boot script",
  "List connected clouds": "List connected clouds",
  "List repositories": "List repositories",
  "Open a pull request changing access.yaml": "Change access policies",
  "Read the organization's access.yaml": "Read access policies",
};

const CREDENTIALS_DESCRIPTION =
  "The cloud's credentials. For `aws`: `access_key`, `secret_key` and " +
  "optionally `region`. For `gcp`: the fields of the service account key " +
  "file, and optionally `region` (a zone or a region). For `azure`: " +
  "`subscription_id`, `tenant_id`, `client_id`, `client_secret` and " +
  "optionally `region`.";

/** Rewrites applied to `description` strings, longest match first. */
const DESCRIPTIONS = {
  "Airship runs GitHub Actions jobs on ephemeral machines in your own cloud account, configured by an airship.yaml in each repository. The TypeScript client in packages/client is generated from this document.":
    "Airship runs every GitHub Actions job on a fresh machine in your own " +
    "cloud account, configured by an `airship.yaml` in each repository.\n\n" +
    "This reference covers the token-authenticated API for scripting Airship " +
    "itself: activating repositories, connecting clouds and editing runner " +
    "access policies. Create a token under **API keys** in the dashboard and " +
    "send it as `Authorization: Bearer asa_...`.",
  "Cloud account ID (AWS account, GCP project, Azure subscription, Daytona organization)":
    "The cloud's own account identifier: an AWS account ID, a Google Cloud project ID or an Azure subscription ID",
  "Credentials; for aws: access_key, secret_key and optionally region; for gcp: the service account key JSON's fields and optionally region (a zone or region); for azure: subscription_id, tenant_id, client_id, client_secret and optionally region; for daytona: api_key and optionally api_url and region":
    CREDENTIALS_DESCRIPTION,
  "Public half of the credential (AWS access key ID, GCP service account email, Azure application ID, masked Daytona API key)":
    "The public half of the credential: an AWS access key ID, a Google Cloud service account email or an Azure application ID",
  "Region (or GCP zone, or Daytona region) used when a config names none":
    "The region runners launch in when a runner definition names none; on Google Cloud, a zone or a region",
  "Who the credential is, such as an IAM ARN or an API key's name":
    "Who the credential is, such as an IAM user's ARN",
};

const FORBIDDEN = /daytona|cirun/i;

/** Walks every string in the document, applying the rewrites above. */
function rewrite(node, path = "") {
  if (Array.isArray(node)) {
    return node.map((item, index) => rewrite(item, `${path}[${index}]`));
  }
  if (node === null || typeof node !== "object") {
    return node;
  }

  const out = {};
  for (const [key, value] of Object.entries(node)) {
    const at = path ? `${path}.${key}` : key;

    if (key === "enum" && Array.isArray(value) && value.includes("daytona")) {
      out[key] = value.filter((entry) => CLOUDS.includes(entry));
      continue;
    }
    if (
      key === "description" &&
      typeof value === "string" &&
      DESCRIPTIONS[value]
    ) {
      out[key] = DESCRIPTIONS[value];
      continue;
    }
    if (key === "summary" && typeof value === "string") {
      const plain = value;
      if (!(plain in PROSE)) {
        throw new Error(
          `summary at ${at} has no documented wording: ${JSON.stringify(plain)}.\n` +
            "Add it to PROSE in apps/docs/scripts/openapi.mjs."
        );
      }
      out[key] = PROSE[plain];
      continue;
    }
    out[key] = rewrite(value, at);
  }

  return out;
}

/** Collects every `$ref` target reachable from a node. */
function refsIn(node, found = new Set()) {
  if (Array.isArray(node)) {
    for (const item of node) {
      refsIn(item, found);
    }

    return found;
  }
  if (node === null || typeof node !== "object") {
    return found;
  }
  for (const [key, value] of Object.entries(node)) {
    if (key === "$ref" && typeof value === "string") {
      found.add(value.replace("#/components/schemas/", ""));
      continue;
    }
    refsIn(value, found);
  }

  return found;
}

const source = JSON.parse(await readFile(SOURCE, "utf8"));

const paths = {};
for (const path of PUBLIC_PATHS) {
  if (!source.paths[path]) {
    throw new Error(`apps/api/openapi.json no longer serves ${path}.`);
  }
  paths[path] = source.paths[path];
}

// Keep only the schemas the kept paths reach, following refs until closed.
const { schemas, securitySchemes } = source.components;
const keep = refsIn(paths);
for (let added = true; added; ) {
  added = false;
  for (const name of [...keep]) {
    if (!schemas[name]) {
      throw new Error(`openapi.json references a missing schema: ${name}.`);
    }
    for (const nested of refsIn(schemas[name])) {
      if (!keep.has(nested)) {
        keep.add(nested);
        added = true;
      }
    }
  }
}

const document = rewrite({
  components: {
    schemas: Object.fromEntries(
      Object.entries(schemas)
        .filter(([name]) => keep.has(name))
        .sort(([a], [b]) => a.localeCompare(b))
    ),
    securitySchemes,
  },
  info: {
    ...source.info,
    title: "Airship developer API",
  },
  openapi: source.openapi,
  paths,
  security: [{ apiToken: [] }],
  servers: [
    { description: "Airship", url: "https://api.airshipci.com" },
    {
      description: "A control plane you run yourself",
      url: "https://{host}",
      variables: { host: { default: "api.example.com" } },
    },
  ],
  tags: [
    {
      description: "Scripting Airship with a token from the dashboard.",
      name: "Developer API",
    },
  ],
});

const serialized = `${JSON.stringify(document, null, 2)}\n`;

// The published document must not name anything the documentation does not.
const leak = serialized.split("\n").findIndex((line) => FORBIDDEN.test(line));
if (leak !== -1) {
  throw new Error(
    `generated spec line ${leak + 1} names something the docs do not cover:\n` +
      `  ${serialized.split("\n")[leak].trim()}\n` +
      "Add a rewrite to apps/docs/scripts/openapi.mjs."
  );
}

await mkdir(dirname(TARGET), { recursive: true });
await writeFile(TARGET, serialized);

const endpoints = Object.values(paths).reduce(
  (total, methods) => total + Object.keys(methods).length,
  0
);
process.stdout.write(
  `apps/docs/api-reference/openapi.json: ${endpoints} endpoints, ${keep.size} schemas\n`
);
