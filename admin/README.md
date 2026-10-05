# Herald admin

Private admin UI for Herald: service health, applications, and producer
credentials. React, TypeScript, Vite, shadcn/ui, TanStack Router, and TanStack
Query. It calls the admin API in [docs/admin-api.md](../docs/admin-api.md) and
deploys independently of the Go service.

## Local development

Requires Node.js 22+. Start Herald on `:8080` with
`ADMIN_ORIGIN=http://localhost:5173`, then:

```sh
cd admin
npm ci
npm run dev   # http://localhost:5173
```

To work without Herald, run the mock API and point the UI at it:

```sh
npm run mock                                          # admin / herald-dev
VITE_ADMIN_API_URL=http://localhost:4011/api npm run dev
```

`VITE_ADMIN_API_URL` is compiled into the public bundle and the build fails
without it. Never put a secret in a `VITE_*` variable.

## E2E tests

```sh
npx playwright install chromium   # once
npm run test:e2e
npm run test:e2e:report
```

Playwright builds the bundle in `e2e` mode, serves it on `:4173`, and starts
the mock API on `:4012`: cross-origin but same-site, like production, so
credentialed CORS and the `SameSite=Strict` cookie are exercised for real.
The HTML report in `playwright-report/` includes a screenshot for every test.

## Cloudflare deployment

Static-assets Worker, configured in `wrangler.jsonc`. Workers Builds settings:

| Setting | Value |
| ------- | ----- |
| Project name | `herald-admin` (must match `wrangler.jsonc`) |
| Path | `admin` |
| Build command | `npm ci && npm run build` |
| Deploy command | `npx wrangler deploy` |
| Build variable | `VITE_ADMIN_API_URL=https://herald.example.com/api` |
| Custom domain | `admin.herald.example.com` |

`public/_headers` sets the CSP; its `connect-src` must include the API origin.

Only `admin.herald.example.com` can sign in: Herald accepts one `ADMIN_ORIGIN`
and the session cookie is `SameSite=Strict`. Do not put Cloudflare Access in
front of `herald.example.com/api/*`; see the contract.
