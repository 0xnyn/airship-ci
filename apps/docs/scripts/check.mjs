// Checks the documentation holds together, without needing the network.
//
//   1. every page the navigation lists exists on disk
//   2. every page on disk is reachable from the navigation
//   3. every page declares a title and a description
//   4. every internal link points at a page the navigation knows
//   5. nothing names a product or vendor the documentation does not cover
//
// Run by `pnpm lint` and `pnpm typecheck`. `pnpm --filter @airship/docs links`
// adds Mintlify's own checker, which also validates external URLs.

import { readdir, readFile } from "node:fs/promises";
import { dirname, join, posix, relative } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const problems = [];
const fail = (where, message) => problems.push(`${where}: ${message}`);

/**
 * Names the site does not use. Airship's clouds, file names and labels are its
 * own, and prose that reaches for someone else's reads as borrowed.
 */
const FORBIDDEN = [
  { instead: "write Airship's own name", pattern: /\bcirun\b/i },
  {
    instead: "the docs cover AWS, Google Cloud and Azure",
    pattern: /\bdaytona\b/i,
  },
  { instead: "airship.yaml", pattern: /\.cirun\.ya?ml/i },
  { instead: "the developer API", pattern: /\bcirun-py\b/i },
  { instead: "AIRSHIP_API_KEY", pattern: /\bCIRUN_API/i },
];

/** An OpenAPI endpoint entry in the navigation, rather than a page file. */
const ENDPOINT = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) \//;
const FRONTMATTER = /^---\n([\s\S]*?)\n---/;
const MDX_LINK = /\]\(([^)\s]+)\)/g;
const HREF = /href="([^"]+)"/g;
const ABSOLUTE = /^(https?:|mailto:|#|\$)/;
const MDX = /\.mdx$/;
const TRAILING_SLASH = /\/$/;
const LEADING_SLASH = /^\//;

/** Every `.mdx` file under the site root, as a navigation path. */
async function mdxFiles(dir = root) {
  const entries = await readdir(dir, { withFileTypes: true });
  const found = [];
  const nested = [];

  for (const entry of entries) {
    if (entry.name === "node_modules" || entry.name.startsWith(".")) {
      continue;
    }
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      nested.push(mdxFiles(full));
    } else if (entry.name.endsWith(".mdx")) {
      found.push(relative(root, full).replace(MDX, ""));
    }
  }

  return found.concat(...(await Promise.all(nested)));
}

/** Every page path a navigation tree lists, in reading order. */
function pagesIn(node, found = []) {
  if (typeof node === "string") {
    if (!ENDPOINT.test(node)) {
      found.push(node.replace(LEADING_SLASH, ""));
    }

    return found;
  }
  if (Array.isArray(node)) {
    for (const item of node) {
      pagesIn(item, found);
    }

    return found;
  }
  if (node && typeof node === "object") {
    for (const key of ["tabs", "groups", "pages", "anchors", "dropdowns"]) {
      if (node[key]) {
        pagesIn(node[key], found);
      }
    }
  }

  return found;
}

/** Reports any name the documentation does not use. */
function checkNames(file, source) {
  const lines = source.split("\n");
  for (const { pattern, instead } of FORBIDDEN) {
    const line = lines.findIndex((text) => pattern.test(text));
    if (line !== -1) {
      fail(file, `line ${line + 1} names ${pattern.source} — ${instead}`);
    }
  }
}

/** Checks one page's frontmatter, names and internal links. */
function checkPage(page, source, pages) {
  const file = `${page}.mdx`;
  const frontmatter = source.match(FRONTMATTER);

  if (frontmatter) {
    for (const field of ["title", "description"]) {
      if (!new RegExp(`^${field}:\\s*\\S`, "m").test(frontmatter[1])) {
        fail(file, `frontmatter has no ${field}`);
      }
    }
  } else {
    fail(file, "has no frontmatter");
  }

  checkNames(file, source);

  const links = [...source.matchAll(MDX_LINK), ...source.matchAll(HREF)].map(
    ([, link]) => link
  );

  for (const link of links) {
    if (ABSOLUTE.test(link)) {
      continue;
    }
    const resolved = link.startsWith("/")
      ? link.slice(1)
      : posix.normalize(posix.join(posix.dirname(page), link));
    const [target] = resolved.split("#");

    if (target && !pages.has(target.replace(TRAILING_SLASH, ""))) {
      fail(file, `links to ${link}, which is not a page in docs.json`);
    }
  }
}

const rawConfig = await readFile(join(root, "docs.json"), "utf8");
checkNames("docs.json", rawConfig);
const config = JSON.parse(rawConfig);
const listed = pagesIn(config.navigation);
const onDisk = await mdxFiles();
const known = new Set(listed);

for (const page of listed) {
  if (!onDisk.includes(page)) {
    fail("docs.json", `lists ${page}, which has no ${page}.mdx`);
  }
}
for (const page of onDisk) {
  if (!known.has(page)) {
    fail(`${page}.mdx`, "is not listed in docs.json, so nothing links to it");
  }
}

const duplicates = listed.filter(
  (page, index) => listed.indexOf(page) !== index
);
for (const page of new Set(duplicates)) {
  fail("docs.json", `lists ${page} more than once`);
}

const sources = await Promise.all(
  onDisk.map((page) => readFile(join(root, `${page}.mdx`), "utf8"))
);
for (const [index, page] of onDisk.entries()) {
  checkPage(page, sources[index], known);
}

try {
  const spec = await readFile(
    join(root, "api-reference", "openapi.json"),
    "utf8"
  );
  JSON.parse(spec);
  checkNames("api-reference/openapi.json", spec);
} catch (error) {
  fail("api-reference/openapi.json", `${error.message} — run pnpm generate`);
}

if (problems.length > 0) {
  process.stderr.write(`${problems.map((line) => `  ${line}`).join("\n")}\n`);
  process.stderr.write(`\n${problems.length} problem(s) in apps/docs.\n`);
  process.exit(1);
}

process.stdout.write(
  `apps/docs: ${onDisk.length} pages, every link and name checked\n`
);
