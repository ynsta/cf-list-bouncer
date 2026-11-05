# Code map

| Path | Responsibility |
|------|----------------|
| `cmd/cf-list-bouncer/main.go` | Wiring: config, logger, clients, syncer, ops server, signal handling |
| `internal/config/` | Environment variables → validated `Config`; secrets from env or `*_FILE` |
| `internal/lapi/` | CrowdSec LAPI client: full snapshot of active ip/range decisions |
| `internal/blocklist/` | Pure domain: decisions → normalized, deduplicated, capped list items |
| `internal/cloudflare/` | Cloudflare Lists API: replace all items, poll the bulk operation |
| `internal/syncer/` | Reconciliation loop, push decision, failure counting, health, metrics |
| `internal/server/` | `GET /healthz`, `GET /metrics` |
| `scripts/check-public.sh` | Pre-commit guard against private content in this public repo |
| `scripts/check-docs.sh` | Doc indexes, relative links, transient-file residue |
| `.ko.yaml` | Container image build (ko, distroless, pinned digest) |
| `.github/workflows/` | `ci.yml` gates; `release.yml` binaries + SBOM + signatures; `image.yml` ghcr image |
