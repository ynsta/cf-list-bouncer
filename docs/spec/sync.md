# Sync behaviour

Source of truth for what the bouncer does. Code: `internal/blocklist`, `internal/syncer`.

## 1. Input

Each sync reads the **full set** of active decisions from the LAPI stream endpoint with
`startup=true`, `scopes=ip,range`, and `origins=<ORIGINS>`.

## 2. Which decisions become list items

A decision becomes an item only if all hold, checked in this order; otherwise it is
counted under the reason in brackets (metric `cf_list_bouncer_decisions_skipped{reason}`):

1. its type is `ban`, case-insensitive [`not_ban`];
2. its scope is `Ip` or `Range`, case-insensitive [`unsupported_scope`];
3. its value parses as an address (`Ip`) or CIDR (`Range`) [`invalid_value`];
4. after normalization (§3) the prefix is at least `/8` for IPv4 and `/4` for IPv6
   [`too_wide`];
5. the address is publicly routable: not private, loopback, link-local, unspecified or
   multicast [`not_public`].

`not_ban` and `unsupported_scope` are logged at DEBUG (captcha decisions, country bans are
normal). The other reasons are logged at WARN.

## 3. Normalization

- IPv4-mapped IPv6 addresses (`::ffff:a.b.c.d`) become IPv4; zones are dropped.
- IPv6 addresses and IPv6 ranges narrower than `IPV6_PREFIX` (default 64) are widened to
  `IPV6_PREFIX`: a client rotating addresses inside its `/64` stays blocked.
- Ranges are masked to their network address (`203.0.113.7/24` → `203.0.113.0/24`).
- A full-length prefix is written as a bare address (`198.51.100.7/32` → `198.51.100.7`).
- Items are deduplicated after normalization; the first decision's scenario is kept as the
  comment `crowdsec: <scenario>`, truncated to 100 characters.
- Items are sorted by their string form. If more than `MAX_ITEMS` remain, the list is cut
  to the first `MAX_ITEMS` and the rest counted as `over_limit` (WARN).

## 4. When the list is pushed

The list is **replaced as a whole** (one bulk operation) when any holds:

1. nothing has been pushed yet since the process started;
2. the set of addresses differs from the last successful push (comments are ignored);
3. `RESYNC_INTERVAL` > 0 and at least that long has passed since the last push.

Otherwise the sync counts as `unchanged`. An empty set is pushed like any other: when the
last ban expires, the list becomes empty.

A push is successful only once Cloudflare reports the bulk operation `completed` (polled
every 2 s, up to 2 min).

## 5. Failures

- A LAPI error or an undecodable LAPI response ends the sync **without any call to
  Cloudflare**.
- A Cloudflare error (API error, operation `failed`, timeout) ends the sync; the next sync
  pushes again even if the set did not change.
- A Cloudflare `429` (rate limit, e.g. error `10040`) is counted as
  `syncs_total{result="rate_limited"}`, not `error`, and delays the next sync: after the
  n-th rate limit in a row, wait `SYNC_INTERVAL × 2ⁿ`, capped at 30 min, and never less than
  the response's `Retry-After` (capped at 24 h). Retrying at the normal pace only extends
  the limit.
- Each failed sync, rate-limited or not, increments `cf_list_bouncer_consecutive_failures`;
  a success resets it. When the failures **other than rate limits** reach
  `MAX_CONSECUTIVE_FAILURES` (> 0), the process exits with status 1. Rate limits never stop
  the process: a restart would push again at once.
- Errors and logs never contain the LAPI key or the Cloudflare token.

## 6. Schedule and shutdown

The first sync runs at startup, then `SYNC_INTERVAL` after the end of each sync (longer after
a rate limit, §5). SIGINT/SIGTERM stops the loop,
shuts the ops server down within 5 s and exits 0. The list is left as it is.

## 7. Health

`GET /healthz` returns `200 ok` when the last successful sync is at most 3 × `SYNC_INTERVAL`
old; before the first success, the same delay counts from process start. Otherwise `503`.
