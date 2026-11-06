# cf-list-bouncer

A [CrowdSec](https://www.crowdsec.net/) bouncer that blocks banned IPs **at the Cloudflare
edge**. It keeps a Cloudflare account IP List equal to your active local CrowdSec bans; one
WAF custom rule on that list does the blocking. Banned clients never reach your server.

```
CrowdSec LAPI ──(every 30 s: active bans)──▶ cf-list-bouncer ──(replace list)──▶ Cloudflare IP List
                                                                                     │
                                       WAF custom rule:  ip.src in $edge_bans → Block
```

Why a list and not a Worker: a WAF rule on a list costs nothing per request, works on the
free plan, and keeps working if the bouncer stops (the last list stays in place). Keep your
usual bouncer on the server too (reverse-proxy plugin, firewall): it covers the seconds
between a ban and the next sync.

## How it works

- Every `SYNC_INTERVAL` (30 s), it reads the **full** set of active `ban` decisions of
  scope `Ip` and `Range` from the LAPI, filtered by origin (default `crowdsec,cscli`: your
  own detections and manual bans, not the community blocklist).
- It normalizes them into list items: IPv6 addresses widened to a `/64`, private and
  invalid addresses dropped, duplicates merged, sorted.
- When the result differs from what it last pushed (or every `RESYNC_INTERVAL`, 1 h, to
  undo manual edits), it **replaces the whole list** in one bulk operation and waits for
  Cloudflare to apply it.
- A failed LAPI read never touches the list. Expired or deleted bans disappear from the
  list on the next sync.
- A Cloudflare rate limit (`429`) makes it back off, up to 30 min between attempts, instead
  of retrying every interval.

Details: [behaviour spec](docs/spec/sync.md), [architecture](docs/design/architecture.md).

## Quick start

1. **Cloudflare**: create an IP List, a WAF rule using it, and an API token allowed to edit
   lists ([step by step](docs/sre/cloudflare-setup.md)). **Keep `crowdsec` out of the list
   name**: Cloudflare then allows only one update per 24 hours.
2. **CrowdSec**: register the bouncer and keep the key:
   ```sh
   cscli bouncers add cf-list-bouncer
   ```
3. **Configure**: copy the example and fill in the four required values:
   ```sh
   cp .env.example .env && chmod 600 .env
   ```
4. **Run** it on the Docker network where CrowdSec runs (Compose example in
   [docs/sre/deploy.md](docs/sre/deploy.md)):
   ```sh
   docker run -d --name cf-list-bouncer --restart unless-stopped \
     --network <crowdsec network> --env-file .env \
     ghcr.io/ynsta/cf-list-bouncer:latest
   ```

## Configuration

All configuration comes from environment variables. Invalid values stop the process at
startup with every error listed.

| Variable | Default | Description |
|----------|---------|-------------|
| `CROWDSEC_LAPI_URL` | required | LAPI base URL, e.g. `http://crowdsec:8080`. |
| `CROWDSEC_LAPI_KEY` / `CROWDSEC_LAPI_KEY_FILE` | required | Bouncer key from `cscli bouncers add`, or a file holding it. Set one, not both. |
| `CF_API_TOKEN` / `CF_API_TOKEN_FILE` | required | Cloudflare API token with *Account Filter Lists: Edit*, or a file holding it. |
| `CF_ACCOUNT_ID` | required | Cloudflare account ID (32 lowercase hex). |
| `CF_LIST_ID` | required | IP List ID (32 lowercase hex). |
| `CF_API_URL` | `https://api.cloudflare.com/client/v4` | API root; must be https. |
| `ORIGINS` | `crowdsec,cscli` | Comma-separated decision origins to sync. Add e.g. `lists:<name>` to include a blocklist. |
| `SYNC_INTERVAL` | `30s` | Time between syncs (minimum `10s`). |
| `RESYNC_INTERVAL` | `1h` | Push an unchanged list this often, to undo drift. `0s` disables. |
| `IPV6_PREFIX` | `64` | IPv6 addresses and narrower ranges are widened to this length (4–64). |
| `MAX_ITEMS` | `10000` | List size cap. Set it to your Cloudflare plan's limit. |
| `LISTEN_ADDR` | `:9090` | Address of `/healthz` and `/metrics`. |
| `MAX_CONSECUTIVE_FAILURES` | `20` | Exit after this many failed syncs in a row (`0` = never); let the restart policy recover. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. Logs are JSON on stdout. |

## Operations

- `GET /healthz`: `200` when a sync succeeded within the last 3 intervals, `503` otherwise.
- `GET /metrics`: Prometheus metrics prefixed `cf_list_bouncer_` (list size, syncs by
  result (`pushed`, `unchanged`, `error`, `rate_limited`), last success, skipped decisions
  by reason). Suggested alerts in
  [docs/sre/deploy.md](docs/sre/deploy.md).

## Verifying a release

Binaries ship with a CycloneDX SBOM; their `SHA256SUMS` is signed with cosign (keyless,
Sigstore bundle `SHA256SUMS.cosign.bundle`). Each GitHub Release lists the verification
command. The container image is signed too:

```sh
cosign verify ghcr.io/ynsta/cf-list-bouncer:<tag> \
  --certificate-identity-regexp 'https://github.com/ynsta/cf-list-bouncer/.+' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

## Development

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md). Contributions follow [AGENTS.md](AGENTS.md).

## License

[Apache-2.0](LICENSE)
