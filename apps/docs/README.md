# @airship/docs

Airship's user-facing documentation, published with
[Mintlify](https://mintlify.com). Pages are MDX; `docs.json` holds the
navigation, theme and API settings.

```bash
pnpm --filter @airship/docs dev     # preview on http://localhost:4100
pnpm --filter @airship/docs lint    # navigation, links, frontmatter, names
pnpm --filter @airship/docs links   # Mintlify's own checker (also hits the web)
```

`make docs` and `make docs-check` wrap the first two.

## Layout

| Path | What it is |
| --- | --- |
| `docs.json` | Navigation, theme, navbar, footer, API playground |
| `index.mdx`, `quickstart.mdx`, `move-workflows.mdx`, `how-it-works.mdx`, `concepts.mdx` | Getting started |
| `images/dashboard/` | Dashboard screenshots as WebP, a `-light` and a `-dark` of each, shown in a `<Frame>` with `dark:` classes. Retake them when the screens they show change |
| `clouds/` | Connecting AWS, Google Cloud and Azure: credentials, policies, quotas |
| `sandboxes/` | The sandbox clouds (Daytona, Runloop, E2B, Novita, Lelantos, Namespace, Tenki, Modal): an overview and a page each |
| `configuration/` | The `airship.yaml` reference, a page per concern |
| `performance/` | Caching, idle runners, custom images |
| `organization/` | Organization-wide runners, access control, capacity |
| `dashboard.mdx`, `troubleshooting.mdx`, `security.mdx` | Running Airship |
| `self-hosting/` | Running the control plane yourself |
| `api-reference/` | The developer API: one hand-written page, the rest generated |
| `scripts/` | The spec generator and the consistency checker |
| `favicon.svg`, `logo/` | **Generated** from `@airship/tokens` by `pnpm generate`; never edit |
| `og/background.jpg` | The backdrop every page's share thumbnail is drawn over; rendered by `pnpm --filter @airship/home media` |

## The API reference is generated

`scripts/openapi.mjs` builds `api-reference/openapi.json` from
`apps/api/openapi.json`: it keeps the token-authenticated paths under `/api`,
drops the schemas nothing reachable references, and rewrites the handlers'
own wording for an API audience. Mintlify renders the endpoint pages from it.

It runs as part of `pnpm generate`, and `pnpm check:generated` fails if the
committed file is stale. **Never edit `api-reference/openapi.json` by hand.** If the Go API
grows a summary the script has no wording for, the script fails and names it —
add the wording rather than letting prose written for the codebase reach the
site.

## Writing

- Every page needs `title` and `description` in its frontmatter; the checker
  enforces it.
- Internal links are root-relative (`/configuration/labels`) and checked
  against `docs.json`, so a renamed page fails the build rather than 404ing.
- A page that is not listed in `docs.json` is a failure too — nothing should
  be unreachable.
- The checker also refuses Cirun's names, so prose stays Airship's own. Name
  only the clouds the docs cover; a new sandbox cloud gets its page under
  `sandboxes/` and a row in the overview's table.
- Components in use: `Columns`/`Card`, `Steps`/`Step`, `Tabs`/`Tab`,
  `Accordion`/`AccordionGroup`, `CodeGroup`, `ParamField`/`ResponseField`,
  `Note`/`Tip`/`Warning`, and `mermaid` code blocks.

The internal engineering documents under `docs/` are the source of truth for
behaviour. When behaviour changes, update both.
