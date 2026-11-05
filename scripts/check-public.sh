#!/usr/bin/env bash
# Pre-commit guard for a public repository: refuses a commit whose staged changes match
# a forbidden pattern (private hostnames, employer names, personal emails...).
#
# The pattern list lives OUTSIDE the repository, one extended regex per line, so the
# forbidden words never get published themselves:
#   ${CHECK_PUBLIC_PATTERNS:-$HOME/.config/cf-list-bouncer/forbidden-patterns}
#
# Install as a hook:  ln -s ../../scripts/check-public.sh .git/hooks/pre-commit
# Run by hand:        scripts/check-public.sh            (staged changes)
#                     scripts/check-public.sh --all      (every tracked file)
set -euo pipefail

patterns="${CHECK_PUBLIC_PATTERNS:-$HOME/.config/cf-list-bouncer/forbidden-patterns}"
if [ ! -s "$patterns" ]; then
  echo "check-public: no pattern file at $patterns, skipping" >&2
  exit 0
fi

if [ "${1:-}" = "--all" ]; then
  hits=$(git grep -n -i -E -f "$patterns" -- . ':(exclude)vendor' || true)
else
  # Only added lines, without the diff headers. The author identity is checked too:
  # a commit email is published with the commit.
  hits=$(git diff --cached --no-color -U0 -- . ':(exclude)vendor' | grep -E '^\+[^+]' | grep -n -i -E -f "$patterns" || true)
  ident=$(git var GIT_AUTHOR_IDENT; git var GIT_COMMITTER_IDENT)
  if grep -q -i -E -f "$patterns" <<<"$ident"; then
    hits="${hits}"$'\n'"commit identity matches a forbidden pattern"
  fi
fi

if [ -n "${hits//[[:space:]]/}" ]; then
  echo "check-public: forbidden content found, commit refused:" >&2
  echo "$hits" >&2
  exit 1
fi
