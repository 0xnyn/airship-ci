# Step 5 — The dashboard on Vercel

**What this is.** `apps/dash` on Vercel, on `app.example.com`. The
dashboard is a client-side app: the browser talks to the API directly, and
the Vercel deployment never talks to the API itself. So Vercel only has to
serve the pages, and everything with state lives on Render and Supabase.

**Time.** 10 minutes, plus DNS.

**Prerequisite.** Step 4: the API answering on `https://api.example.com`.

---

## Create the project

**Add New → Project**, import this repository, then:

| Setting | Value |
| --- | --- |
| Root Directory | `apps/dash` |
| Framework Preset | Next.js (detected) |
| Build, install and output commands | leave as detected |
| Node.js Version (Settings → Build and Deployment) | **24.x**, as `engines` in the root `package.json` asks |

Vercel installs from the repository root, because `apps/dash` is part of
the pnpm workspace, and builds the `@airship/client` and `@airship/ui`
sources along with the app (`transpilePackages`). The generated API client
is committed, so the build doesn't need Go.

---

## The environment

Two variables and one optional one, for **Production** and **Preview**:

| Key | Value | Why |
| --- | --- | --- |
| `NEXT_PUBLIC_API_URL` | `https://api.example.com` | **Baked into the JavaScript at build time.** Varlock (`apps/dash/.env.schema`) fails the build without it, and refuses a localhost URL in a production build. Changing it later means a redeploy |
| `NEXT_PUBLIC_APP_URL` | Optional: `https://app.example.com` | The address link previews fetch the social card from. Unset, Vercel's production URL is used, which is right unless you serve the dashboard on a custom domain |
| `ENABLE_EXPERIMENTAL_COREPACK` | `1` | Makes Vercel use the pnpm version pinned in `packageManager` (`pnpm@11.22.0`). Vercel would otherwise pick a pnpm from the lockfile version, which can ignore settings in `pnpm-workspace.yaml` such as `allowBuilds` |

That is the whole list. The dashboard holds no secrets: every privileged
call goes to the API with the session cookie. The template is
[`deploy/production/dash.env.example`](../../../deploy/production/dash.env.example).

---

## The custom domain

**Settings → Domains → Add** `app.example.com`, then create the record
Vercel shows. On Cloudflare it must be **DNS only**, not proxied.

If the apex `example.com` is your marketing site, leave it alone — only
`app.` belongs to Vercel here.

---

## Preview deployments can't sign in

They're served from `*.vercel.app`, which is a different site from
`api.example.com`, so the session cookie is never sent, and
`CORS_ORIGINS` doesn't list them either. Previews are still useful for
checking that a page renders.

To skip them, add an Ignored Build Step of
`[ "$VERCEL_ENV" != "production" ]`, or `npx turbo-ignore` to skip builds
where `apps/dash` and its packages didn't change.

The `output: "standalone"` setting in `next.config.ts` is for the Docker
image used on stage 1. Vercel uses its own build output and ignores it.

---

## Checkpoint

- [ ] `https://app.example.com` loads the sign-in page.
- [ ] The page offers **Sign in with GitHub** (not only a development
      sign-in). If the button is missing, `/v1/auth/config` on the API
      returned `"githubSignInUrl": null` — which means
      `GITHUB_CLIENT_SECRET` is missing on Render, since that is the one
      value sign-in needs beyond what the API checks at startup.
- [ ] The browser console shows no CORS errors against
      `api.example.com`. One means `CORS_ORIGINS` on Render doesn't match
      this origin exactly.

Signing in is step 6, because it needs the app installed somewhere.

Next: [step 6 — the first real job](6-first-job.md).
