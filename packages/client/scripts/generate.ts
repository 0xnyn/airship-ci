// Turns the Go API's OpenAPI document into TypeScript types.
// Run by `pnpm generate`; `pnpm dev` re-runs it whenever the spec changes.
import { readFile, writeFile } from "node:fs/promises";
import openapiTS, { astToString } from "openapi-typescript";

const spec = new URL("../../../apps/api/openapi.json", import.meta.url);
const target = new URL("../src/generated/api.ts", import.meta.url);

const banner = `// Code generated from apps/api/openapi.json by scripts/generate.ts. DO NOT EDIT.
// Change the Go handlers in apps/api, then run \`pnpm generate\`.

`;

const ast = await openapiTS(spec);
const output = banner + astToString(ast);

const current = await readFile(target, "utf8").catch(() => "");
if (current === output) {
  console.log("client types are up to date");
} else {
  await writeFile(target, output);
  console.log(`wrote ${target.pathname}`);
}
