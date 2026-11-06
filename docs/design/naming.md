# Naming conventions

One row per kind of named thing. Updated in place. Rule and trigger: `AGENTS.md`,
`## Naming Conventions`.

| Kind | Pattern | Example | Derived from the name | Limits | Source |
|------|---------|---------|-----------------------|--------|--------|
| Binary, container image, CrowdSec bouncer name | `cf-list-bouncer` | `ghcr.io/ynsta/cf-list-bouncer` | GitHub repo, Go module path, User-Agent prefix | fixed | decided here |
| Prometheus metric | `cf_list_bouncer_<noun>[_<unit>][_total]` | `cf_list_bouncer_syncs_total` | dashboards, alerts | Prometheus naming rules | decided here |
| Environment variable | `CROWDSEC_*` for LAPI access, `CF_*` for Cloudflare, bare name for behaviour; `<SECRET>_FILE` for a file-held secret | `CF_API_TOKEN_FILE` | operators' compose files | upper snake case | decided here |
| Cloudflare IP List (in docs) | `edge_bans` | `ip.src in $edge_bans` | the WAF rule expression | Cloudflare list name rules (lowercase, digits, `_`); **must not contain `crowdsec`**: Cloudflare limits such lists to one bulk operation per 24 h | decided here |

## Known deviations

None.
