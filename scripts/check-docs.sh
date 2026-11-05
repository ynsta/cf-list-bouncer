#!/usr/bin/env bash
# Documentation consistency check (see AGENTS.md "Documentation policy").
#   - every docs/ subdirectory (except docs/superpowers/) has a 00-index.md;
#   - every relative Markdown link in README.md, AGENTS.md, CHANGELOG.md and docs/
#     points to an existing file;
#   - --strict: docs/superpowers/{specs,plans} hold no leftover transient file.
# Exit 1 on any problem.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

strict=false
[ "${1:-}" = "--strict" ] && strict=true
fail=0

while IFS= read -r dir; do
  case "$dir" in docs/superpowers*) continue ;; esac
  if [ ! -f "$dir/00-index.md" ]; then
    echo "missing index: $dir/00-index.md"; fail=1
  fi
done < <(find docs -mindepth 1 -type d | sort)

while IFS= read -r file; do
  base=$(dirname "$file")
  # [text](target) where target is not a URL, an anchor or a mail link
  while IFS= read -r target; do
    path="${target%%#*}"
    [ -z "$path" ] && continue
    if [ ! -e "$base/$path" ]; then
      echo "broken link: $file -> $target"; fail=1
    fi
  done < <(grep -oE '\]\([^)]+\)' "$file" | sed -E 's/^\]\((.*)\)$/\1/' \
            | grep -vE '^(https?:|mailto:|#)' || true)
done < <(ls README.md AGENTS.md CHANGELOG.md 2>/dev/null; find docs -name '*.md' | sort)

if $strict; then
  leftovers=$(find docs/superpowers -type f ! -name .gitkeep 2>/dev/null || true)
  if [ -n "$leftovers" ]; then
    echo "transient files left in docs/superpowers (fold them into spec/design/adr, then delete):"
    echo "$leftovers"; fail=1
  fi
fi

exit "$fail"
