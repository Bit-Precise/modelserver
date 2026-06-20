# payserver extraction

The `services/payserver/` source tree was removed from this monorepo
and now lives in a standalone repository. This doc explains what
changed, why, and how to set up the new repo if it doesn't exist yet.

## Why

- Payserver has its own deployment cadence (per-merchant config, payment
  channel onboarding) that doesn't track modelserver releases.
- The admin frontend wants the modelserver-dashboard pattern (Vite
  build → COS + CDN via GitHub Actions), which is awkward to share
  workflows with the modelserver image-build pipeline.
- Cross-repo CI is simpler than carving overlapping path filters in one
  monorepo workflow.

## What changed in this monorepo

- `services/payserver/` — entire subtree removed (`git rm -rf`)
- `docker-compose.yml` — payserver service now pulls a prebuilt image
  via `${PAYSERVER_IMAGE:-…}` instead of building from a local path.
  Until the new repo is publishing images, set `PAYSERVER_IMAGE` in
  your shell or `.env` to a working tag.
- `.github/workflows/images.yml` — `payserver` job removed; the
  workflow now only builds modelserver.
- `gateway/nginx.conf` — **unchanged**; it still proxies to the
  `payserver` docker service name (independent of where the image
  comes from).

## Bootstrapping the new payserver repo

A branch with the payserver source as the repo root (no
`services/payserver/` prefix) is preserved in this repo:

```bash
git fetch origin
git checkout payserver-extract        # or just inspect: git log payserver-extract
```

This branch was created via `git subtree split` immediately before the
deletion commit, so it contains the full history of every commit that
touched `services/payserver/` (42 commits at extraction time, head
`6fcb4fd`).

### Push to the new repo

```bash
# 1) Create the empty repo on GitHub (web UI) — let's call it
#    `https://github.com/<ORG>/payserver.git`

# 2) From a clone of THIS monorepo:
git checkout payserver-extract

# 3) Push the subtree-split branch as the new repo's main:
git push https://github.com/<ORG>/payserver.git payserver-extract:main

# 4) (Optional) clean up the extract branch from this monorepo once
#    pushed — it's served its purpose:
git branch -D payserver-extract
git push origin --delete payserver-extract
```

### After the new repo is live

1. **Publish an image to a registry.** Copy `images.yml`'s payserver
   job (deleted from this monorepo in the same commit as this doc was
   added — find it in `git log -- .github/workflows/images.yml` of this
   repo just before the extraction merge) into the new repo's
   `.github/workflows/`. Tag namespace will be different
   (`ghcr.io/<ORG>/payserver:…`).

2. **Update this monorepo's `docker-compose.yml`** to point at the real
   image tag — replace `ghcr.io/PLACEHOLDER-ORG/payserver:latest` with
   the actual one.

3. **(Optional) admin frontend → COS pipeline.** The admin SPA at
   `admin/` in the new repo is currently embedded into the Go binary
   via `//go:embed admin_dist`. To match the modelserver-dashboard
   pattern (build via GH Actions, push to COS, serve from CDN with the
   admin loading cross-origin from the backend), see
   [`.github/workflows/dashboard.yml`](../.github/workflows/dashboard.yml)
   in this repo as the template. The backend changes needed for
   cross-origin admin (CORS allowlist, `SameSite=None` session cookies,
   configurable OIDC post-login redirect URL) were sketched out but not
   landed before extraction — they belong in the new repo's first
   round of refactoring.

## What the new repo inherits

- 42 commits of history (run `git log payserver-extract` in this
  monorepo to see the full list before pushing)
- Standalone Go module at the root (the subtree already had its own
  `go.mod` because it was a self-contained microservice in the
  monorepo)
- Embedded admin SPA (`//go:embed admin_dist` in `cmd/payserver/main.go`)
- Working test suite (run `go test ./...` from the root of the new
  repo — note tests share a Postgres DB and must run sequentially with
  `-p 1`, same as before)
- Migration runner, all four payment gateways (Stripe / WeChat / Alipay
  / plus default-tenant bootstrap), OIDC admin, rescue subcommand

## Verifying the migration

After the new repo is publishing images and `docker-compose.yml` here
points at the real tag:

```bash
docker compose pull payserver
docker compose up postgres modelserver payserver gateway
# Walk through the deployment runbook (the most recent version lives
# in the new payserver repo's docs/, copied at extraction time).
```

Once a full end-to-end payment + webhook cycle works against the
remote-image payserver, the extraction is complete.
