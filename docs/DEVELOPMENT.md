# Development guide

Rules for agents and contributors: [`AGENTS.md`](../AGENTS.md). This guide is the how-to.

## Prerequisites

- Go (version from `go.mod`), `golangci-lint` v2, `govulncheck`, `gh` (GitHub CLI).
- Optional: `ko` to build the image locally, `actionlint` for workflow changes.
- AI workflow: the [superpowers](https://github.com/obra/superpowers) skills.

```sh
ln -s ../../scripts/check-public.sh .git/hooks/pre-commit   # once per clone
```

## Inner loop

```sh
go test -race ./...           # whole flow against in-process LAPI + Cloudflare fakes
golangci-lint run ./...       # must be 0 issues
govulncheck ./...             # after any go.mod change
scripts/check-docs.sh         # doc indexes and relative links
```

Dependencies: `go get <module>@<version> && go mod tidy && go mod vendor`, commit
`vendor/` with the change. CI fails if `go mod tidy && go mod vendor` would change anything.

No CrowdSec and no Cloudflare account are needed: `internal/lapi` and
`internal/cloudflare` are tested against `httptest` servers that mimic the real APIs,
`internal/syncer` against fakes of its two interfaces.

## Run it for real

Use a **dedicated test list** in Cloudflare, never the one a production WAF rule uses, and a
CrowdSec you can add test decisions to:

```sh
go build -o cf-list-bouncer ./cmd/cf-list-bouncer
CROWDSEC_LAPI_URL=http://127.0.0.1:8080 CROWDSEC_LAPI_KEY_FILE=./lapi.key \
CF_API_TOKEN_FILE=./cf.token CF_ACCOUNT_ID=<id> CF_LIST_ID=<test list id> \
LOG_LEVEL=debug ./cf-list-bouncer
```

Keep key files out of git (`.gitignore` covers `.env`; prefer files outside the clone).

Image: `VERSION=dev KO_DOCKER_REPO=ko.local ko build --bare ./cmd/cf-list-bouncer`.

## AI-assisted workflow

All meaningful changes follow:

```
brainstorming → worktree → writing-plans → subagent-driven-development → PR
```

1. **Design**: `/brainstorming` writes a spec to
   `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md` and waits for approval.
2. **Isolate**: `superpowers:using-git-worktrees`, feature branch.
3. **Plan**: `/writing-plans` saves `docs/superpowers/plans/YYYY-MM-DD-<feature>.md`.
4. **Implement**: `superpowers:subagent-driven-development`, one fresh subagent per task,
   test first.
5. **Verify**: `superpowers:verification-before-completion`; no "done" without output.
6. **Submit**: `superpowers:finishing-a-development-branch`: push, open the PR, clean up.

| Situation | Skill |
|-----------|-------|
| Starting a feature or significant change | `/brainstorming` |
| Turning an approved spec into a plan | `/writing-plans` |
| Executing a plan | `superpowers:subagent-driven-development` |
| Bug or unexpected behaviour | `/systematic-debugging` |
| Before marking any task complete | `superpowers:verification-before-completion` |
| Before opening a PR | `/requesting-code-review` |
| Responding to review feedback | `superpowers:receiving-code-review` |

## Branches and pull requests

```
main  (protected: CI required)
 ├── feat/<topic>   new features
 ├── fix/<topic>    bug fixes
 ├── chore/<topic>  deps, CI, config
 └── docs/<topic>   documentation only
```

```sh
git fetch origin && git switch -c feat/my-feature origin/main
# ... commits (Conventional Commits) ...
go test -race ./... && golangci-lint run ./... && scripts/check-docs.sh
git push -u origin feat/my-feature
gh pr create --draft --title "feat: my feature" --body "..."
gh pr ready
```

Before review: README and `docs/**` match the change, `CHANGELOG.md` has an entry under
`[Unreleased]` if users see the change. Squash-merge; the branch is deleted after merge.

Keep branches short-lived and focused; rebase on `origin/main` rather than merging it in.

## Documentation

Layout and rules: `AGENTS.md`, `## Documentation policy`; map: [00-index.md](00-index.md).
`spec/` (WHAT) and `design/` (HOW) are current state, edited in place; `adr/` holds the
WHY. Transient agent output goes to `docs/superpowers/` and is deleted once folded in.
Closing a work cycle: update spec/design/CODEMAP, delete transient files, run
`scripts/check-docs.sh --strict`, write `.agents/last-docs-consolidate`.

## Release

1. Pre-release sweep (`AGENTS.md`, `## Documentation Coherence`).
2. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, add a fresh
   `## [Unreleased]`, update the links at the bottom. Merge that through a PR.
3. From an up-to-date, clean `main`:
   ```sh
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z
   ```
   Push the tag from your machine: a tag pushed with `GITHUB_TOKEN` does not trigger
   workflows.
4. `release.yml` re-runs every gate, builds the Linux binaries with SBOMs, signs
   `SHA256SUMS` with cosign and publishes the GitHub Release with the CHANGELOG section as
   notes. `image.yml` pushes and signs `ghcr.io/ynsta/cf-list-bouncer:X.Y.Z` and `latest`.
5. First release only: make the ghcr.io package public (package settings on GitHub).

Dry run before a real release: tag `v0.0.0-rc1`, check both workflows, delete the
release, the tag and the image version afterwards.
