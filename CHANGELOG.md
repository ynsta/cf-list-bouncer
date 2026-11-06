# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.1] - 2026-11-06

### Fixed

- A Cloudflare rate limit (HTTP `429`) no longer triggers a retry every `SYNC_INTERVAL` and,
  after `MAX_CONSECUTIVE_FAILURES`, an exit and an immediate push on restart. The bouncer
  now backs off (interval doubled per rate limit, capped at 30 min, `Retry-After` honoured)
  and keeps running. Rate limits are counted as `syncs_total{result="rate_limited"}`.
- Docs suggested naming the list `crowdsec_bans`. Cloudflare limits lists it attributes to
  a CrowdSec integration to one update per 24 hours, so every push after the first was
  refused. The suggested name is now `edge_bans`, and the docs warn against `crowdsec` in
  the name.

## [0.1.0] - 2026-11-05

### Added

- Sync of active CrowdSec `Ip` and `Range` bans into a Cloudflare IP List, filtered by
  decision origin (local detections and manual bans by default).
- IPv6 bans widened to a `/64`, so a client rotating addresses stays blocked.
- Periodic full resync that undoes manual edits of the list.
- `/healthz` and Prometheus `/metrics` endpoints.
- Configuration from environment variables, with `*_FILE` variants for secrets; invalid
  configuration stops the process at startup.
- Signed Linux binaries with SBOM, and a signed multi-arch container image on ghcr.io.
- `.env.example` listing every setting with its default.

[Unreleased]: https://github.com/ynsta/cf-list-bouncer/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/ynsta/cf-list-bouncer/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/ynsta/cf-list-bouncer/releases/tag/v0.1.0
