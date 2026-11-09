# Architecture

```
            ┌────────────── cf-list-bouncer ──────────────┐
 CrowdSec   │  lapi.Client ──▶ syncer.Syncer ──▶ cloudflare.Client │   Cloudflare
 LAPI  ◀────┼──── GET stream      │   ▲          PUT items ───────┼──▶ Lists API
            │                     ▼   │          GET bulk op      │
            │              blocklist.Build (pure)                 │
            │  server: /healthz (Syncer.Healthy), /metrics        │
            └─────────────────────────────────────────────────────┘
```

## Components

- **`lapi.Client`** asks the stream endpoint with `startup=true` on every sync, so each
  call returns the complete current set. No delta bookkeeping, nothing to lose on restart.
  It also sends `dedup=false`: the default keeps one decision per address across all
  origins, which hides a local ban behind a longer community one (`docs/spec/sync.md` §1).
- **`blocklist.Build`** is pure: decisions in, sorted items and skip counts out. All the
  normalization rules of `docs/spec/sync.md` live here and nowhere else.
- **`syncer.Syncer`** owns the loop and the only mutable state: the last pushed item set,
  the time of the last push and of the last success, the failure and rate-limit counters,
  and the backoff after a rate limit. It reaches the
  edges through two one-method interfaces (`Source`, `Sink`), which the tests fake.
- **`cloudflare.Client`** replaces all items with one `PUT` and polls the asynchronous bulk
  operation until `completed` or `failed`. A `429` comes back as a `RateLimitError`
  carrying `Retry-After`; the syncer sees it through its `RateLimited` interface, without
  importing the package.
- **`server`** exposes health and metrics; it has no other routes.

## Why the whole list is replaced

See [ADR 0001](../adr/0001-full-list-replace-from-snapshot.md). In short: full snapshot +
full replace is idempotent, survives restarts and manual edits, and needs no
per-item IDs. The cost, one bulk operation per change, is small at local-ban volumes, as
long as the list name does not contain `crowdsec`: Cloudflare limits lists it attributes to
a CrowdSec integration to one bulk operation per 24 hours (see `docs/sre/cloudflare-setup.md`).

## State and restarts

Nothing is persisted. After a restart the first sync always pushes (no "last pushed" in
memory), which also repairs any drift that happened while the bouncer was down.

## Security notes

- The process holds two secrets: the LAPI bouncer key (read-only access to decisions) and a
  Cloudflare token that should only carry *Account Filter Lists: Edit*. It never logs them.
- Plain `net/http` clients, no SDKs: small dependency surface for a process holding a token.
- The container runs as non-root on a distroless static base.
