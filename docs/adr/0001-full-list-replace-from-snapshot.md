# 0001. Replace the whole list from a full LAPI snapshot

**Status:** accepted
**Date:** 2026-11-05

## Context

The bouncer must make a Cloudflare IP List match the active CrowdSec bans, including
removals when bans expire or are deleted. Options for reading CrowdSec: a notification
plugin, the LAPI stream as deltas, or the stream as a full snapshot. Options for writing
Cloudflare: add/delete individual items, or replace all items.

## Decision

Each sync reads a **full snapshot** (stream endpoint with `startup=true`) and, when the
normalized set changed, **replaces the whole list** with one bulk `PUT`. The process keeps
no persistent state.

## Consequences

- Idempotent and self-healing: a missed update, a restart or a manual edit in the
  dashboard is corrected by the next push (or by `RESYNC_INTERVAL` when nothing changed).
- No mapping between CrowdSec decision IDs and Cloudflare item IDs to maintain.
- Every change costs one bulk operation for the whole list. Fine for local bans (hundreds
  to a few thousand items); with large community blocklists the list cap and API rate
  limits become the constraint, which is why the default origin filter excludes them.
- A failed read must never be turned into an empty push: the syncer only pushes after a
  successful read (`docs/spec/sync.md` §5).

## Alternatives rejected

- **CrowdSec notification plugin**: only fires on new alerts, never on expiry or deletion;
  removals would need a second mechanism.
- **Stream deltas + per-item add/delete**: more API calls, needs item-ID bookkeeping, and
  drifts permanently after any missed delta or manual edit.
- **Cloudflare Worker reading a published blocklist URL**: runs on every request (per-request
  quota on free plans) and makes the ban list public or requires protecting it.
