# KeiRouter Dashboard

The web control surface for KeiRouter, built with React 19, Vite, TypeScript,
and Tailwind CSS 4.

## Development

```bash
npm install
npm run dev      # starts on http://localhost:5180, proxies /api and /v1 to :20180
```

Run the Go backend separately (`cd ../backend && go run ./cmd/keirouter`) so the
proxied API calls resolve.

## Build

```bash
npm run build    # type-checks then emits static assets to dist/
```

In production, the backend serves these assets from a standard install path
(`/usr/local/share/keirouter/frontend/dist` by default) or from the Docker image.

## Design

Paper and ink with the brand blue as the one signal. The rules are in
[`DESIGN.md`](../DESIGN.md) at the repository root; tokens live in
`src/index.css` and primitives in `src/components/ui/`. After changing a colour
token run `npm run contrast`; after adding a provider mark run `npm run sprite`.

## Structure

```
src/
  main.tsx           entry: providers + router
  App.tsx            routes
  lib/api.ts         typed admin API client
  components/        Layout + reusable UI primitives
  pages/             Overview, Providers, Accounts, Chains, Keys, Budgets
