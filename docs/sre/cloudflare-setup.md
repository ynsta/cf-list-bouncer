# Cloudflare setup

Three objects, created once. Dashboard menu names change over time; the API calls below
are the stable reference.

## 1. API token

My Profile → API Tokens → Create Token → **Custom token**:

- Permissions: **Account** · **Account Filter Lists** · **Edit**
- Account resources: **Include** · your account

Nothing else: the bouncer only replaces list items. It does not need zone or WAF rights.
Keep the token in the deployment's `.env` (or a secret file, see `CF_API_TOKEN_FILE`).

Your **account ID** is shown on the account home page and in the URL
(`dash.cloudflare.com/<account id>/...`).

## 2. IP List

Account → Manage Account → Configurations → **Lists** → Create list: type **IP**, name
`edge_bans`. Or with the token from step 1:

```sh
curl -sS -X POST "https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/rules/lists" \
  -H "Authorization: Bearer $CF_API_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"edge_bans","kind":"ip","description":"Managed by cf-list-bouncer, do not edit"}' \
  | jq -r '.result.id'
```

The printed `id` is `CF_LIST_ID`. To find it later:

```sh
curl -sS "https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/rules/lists" \
  -H "Authorization: Bearer $CF_API_TOKEN" | jq -r '.result[] | "\(.id)  \(.name)"'
```

**Do not put `crowdsec` in the list name.** Cloudflare limits lists "kept up to date by a
CrowdSec integration" to one bulk operation per 24 hours
([Lists API](https://developers.cloudflare.com/waf/tools/lists/lists-api/)). A list named
`crowdsec_bans` accepts the first push, then answers `429` `10040 you have been ratelimited`
to every change; the same list named `edge_bans` accepts every push. A list name cannot be
changed: detach the WAF rules, delete the list, create a new one, update `CF_LIST_ID` and the
rules. The free plan allows a single custom list, so the old one must go first.

The list belongs to the bouncer: any manual edit is overwritten at the next push or resync.
Check your plan's limit on list items and set `MAX_ITEMS` to it.

## 3. WAF custom rule (per zone)

For each zone (domain) to protect: Security → WAF → **Custom rules** → Create rule:

- Expression: `(ip.src in $edge_bans)`
- Action: **Block**

Order it before any rule that skips or allows traffic, so a ban always wins.

## Check

Ban a test address you control from outside (a phone on mobile data), wait one
`SYNC_INTERVAL`, and load a site of the zone: Cloudflare answers with its block page
(HTTP 403) and the request never reaches your server.

```sh
cscli decisions add --ip <ip> --duration 5m
curl -sS "https://api.cloudflare.com/client/v4/accounts/$CF_ACCOUNT_ID/rules/lists/$CF_LIST_ID/items" \
  -H "Authorization: Bearer $CF_API_TOKEN" | jq -r '.result[].ip'
```

For an IPv6 client, the list shows its `/64`.
