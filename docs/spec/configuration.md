# Configuration

Environment variables, defaults and meanings: see the table in `README.md` (single list,
kept there for operators). This module holds the validation rules. Code: `internal/config`.

1. Every error is collected; the process prints all of them and exits 1 before doing anything.
2. `CROWDSEC_LAPI_URL` and `CF_API_URL`: absolute `http`/`https` URL with a host, no
   credentials, query or fragment. `CF_API_URL` must be `https`. Trailing `/` is removed.
3. Secrets (`CROWDSEC_LAPI_KEY`, `CF_API_TOKEN`): exactly one of `NAME` and `NAME_FILE`.
   File content and direct values are trimmed; blank after trimming is an error. Error
   messages never include the value.
4. `CF_ACCOUNT_ID`, `CF_LIST_ID`: exactly 32 lowercase hex characters.
5. `ORIGINS`: comma-separated, items trimmed, empty items dropped; at least one required.
6. Durations need a unit (`60` is rejected). `SYNC_INTERVAL` ≥ `10s`; `RESYNC_INTERVAL` ≥ `0s`.
7. `IPV6_PREFIX` integer 4–64; `MAX_ITEMS` 1–1,000,000; `MAX_CONSECUTIVE_FAILURES` 0–1,000,000.
8. `LOG_LEVEL` one of `debug`, `info`, `warn`, `error`.
