# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Sync of active CrowdSec `Ip` and `Range` bans into a Cloudflare IP List, filtered by
  decision origin (local detections and manual bans by default).
- IPv6 bans widened to a `/64`, so a client rotating addresses stays blocked.
- Periodic full resync that undoes manual edits of the list.
- `/healthz` and Prometheus `/metrics` endpoints.
- Configuration from environment variables, with `*_FILE` variants for secrets; invalid
  configuration stops the process at startup.
- Signed Linux binaries with SBOM, and a signed multi-arch container image on ghcr.io.

[Unreleased]: https://github.com/ynsta/cf-list-bouncer/commits/main
