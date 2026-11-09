# Deployment

The bouncer is one stateless container next to CrowdSec. All values below are
placeholders; the real deployment configuration stays private.

## 1. Register with CrowdSec

```sh
docker exec crowdsec cscli bouncers add cf-list-bouncer
```

Put the printed key in `.env` as `CF_LIST_BOUNCER_LAPI_KEY` (it is shown only once).

## 2. Docker Compose

Add the service to the compose file that runs CrowdSec, on the same network. The
variables below come from that stack's `.env`; for a standalone file, replace the
`environment:` block with `env_file: .env` built from `.env.example`.

```yaml
services:
  cf-list-bouncer:
    image: ghcr.io/ynsta/cf-list-bouncer:v0.1.0   # pin a release tag
    container_name: cf-list-bouncer
    restart: unless-stopped
    depends_on:
      - crowdsec
    environment:
      CROWDSEC_LAPI_URL: http://crowdsec:8080
      CROWDSEC_LAPI_KEY: ${CF_LIST_BOUNCER_LAPI_KEY:?}
      CF_API_TOKEN: ${CF_LIST_BOUNCER_CF_TOKEN:?}
      CF_ACCOUNT_ID: ${CF_ACCOUNT_ID:?}
      CF_LIST_ID: ${CF_LIST_ID:?}
    # Metrics and health. Host port: any free one (9090 is often Prometheus itself);
    # bind to loopback unless a remote Prometheus scrapes it.
    ports:
      - "127.0.0.1:9190:9090"
    networks:
      - crowdsec
    read_only: true
    security_opt:
      - no-new-privileges:true
    cap_drop: [ALL]
```

The `:?` makes Compose refuse to start when a value is missing, instead of starting a
bouncer that can only fail.

Start and check:

```sh
docker compose up -d cf-list-bouncer
docker compose logs cf-list-bouncer | tail      # expect "list pushed"
curl -s localhost:9190/healthz                   # ok
```

## 3. Monitoring

Scrape `/metrics` on the published port (container port 9090). Suggested alerts:

```yaml
groups:
  - name: cf-list-bouncer
    rules:
      - alert: CfListBouncerStale
        # No successful sync for 10 minutes: bans no longer reach Cloudflare.
        expr: time() - cf_list_bouncer_last_success_timestamp_seconds > 600
        for: 5m
      - alert: CfListBouncerDown
        expr: up{job="cf-list-bouncer"} == 0
        for: 5m
      - alert: CfListBouncerRateLimited
        # Cloudflare keeps refusing pushes: list name containing "crowdsec", or a stuck list.
        expr: increase(cf_list_bouncer_syncs_total{result="rate_limited"}[1h]) > 3
        for: 15m
      - alert: CfListBouncerListFull
        # Items are being dropped: raise MAX_ITEMS (if the plan allows) or narrow ORIGINS.
        expr: cf_list_bouncer_decisions_skipped{reason="over_limit"} > 0
        for: 15m
```

## 4. Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| exits at start, `invalid configuration` | a variable is missing or malformed | read the listed errors; every one is reported |
| `unexpected LAPI status 403` | wrong or revoked bouncer key | `cscli bouncers list`, re-add, update `.env` |
| `cloudflare API error ... 10000 Authentication error` | token wrong, expired, or lacks *Account Filter Lists: Edit* | recreate the token (see `cloudflare-setup.md`) |
| `push rate limited, backing off`, `429` `10040` on every push | list name contains `crowdsec` (one update per 24 h), or Cloudflare locked the list | rename the list (`cloudflare-setup.md`); if it persists, Cloudflare support |
| `bulk operation failed` | Cloudflare rejected an item or the list is full | check `MAX_ITEMS` against the plan; open an issue with the error text |
| banned IP still reaches the server | WAF rule missing or ordered after an allow rule; the ban is not from a synced origin; or a version before 0.1.2, whose list missed local bans also covered by a longer community decision | check the rule; `cscli alerts list --ip <IP>` for local alerts (`cscli decisions list` also keeps only the longest decision per address, so it may show only `CAPI`); upgrade |
| list contains old bans | the bouncer is not running or failing | `/healthz`, logs; the list is only updated by a running bouncer |
