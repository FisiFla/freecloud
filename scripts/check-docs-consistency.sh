#!/usr/bin/env bash
# check-docs-consistency.sh — fail the build when the documentation drifts
# away from the code.
#
# Two checks:
#
#   1. Every `make <target>` named in the documentation must exist in the
#      Makefile. (A documented target that does not exist sends every
#      contributor down a dead end — that is how `make kc-setup` survived.)
#
#   2. Every variable named in .env.prod.example must actually be read by
#      docker/docker-compose.prod.yml. `--env-file` only provides
#      interpolation variables: a variable that the compose file never
#      references never reaches a container, so documenting it is misleading.
#
# Usage: scripts/check-docs-consistency.sh    (exit 1 on drift)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

MAKEFILE="Makefile"
COMPOSE="docker/docker-compose.prod.yml"
ENV_EXAMPLE=".env.prod.example"

# The documents that are checked.
DOCS="README.md CONTRIBUTING.md SECURITY.md docs/DEPLOYMENT.md docs/QUICKSTART.md docs/OBSERVABILITY.md docs/BACKUP_RESTORE.md docs/E2E.md docs/SECRETS.md docs/ARCHITECTURE.md docs/API.md docs/PARITY.md docs/PILOT_GO_LIVE.md docs/IDP_INTEGRATION.md docs/HANDLERS_OWNERSHIP.md"

FAIL=0

# ── 1. Documented make targets must exist ─────────────────────────────────────

TARGETS=$(grep -oE '^[a-zA-Z0-9_-]+:' "${MAKEFILE}" | sed 's/:$//' | sort -u)

for doc in ${DOCS}; do
  [ -f "${doc}" ] || continue
  while read -r target; do
    [ -n "${target}" ] || continue
    if printf '%s\n' "${TARGETS}" | grep -qx -- "${target}"; then
      continue
    fi
    # Only flag a target when the document shows it as an invocation. A
    # sentence like "there is no `make kc-setup` target" documents the
    # absence on purpose and must not fail the build.
    while IFS= read -r line; do
      [ -n "${line}" ] || continue
      prefix="${line%%"make ${target}"*}"
      case "$(printf '%s' "${prefix}" | tr '[:upper:]' '[:lower:]')" in
        *"no "*|*"not "*|*"never"*|*"don't"*|*"do not"*|*"removed"*|*"instead of"*) continue ;;
      esac
      echo "ERROR: ${doc} documents 'make ${target}' but the Makefile has no such target." >&2
      FAIL=1
    done <<< "$(grep -F "make ${target}" "${doc}" || true)"
  done <<< "$(grep -ohE '\bmake [a-zA-Z0-9_-]+' "${doc}" | awk '{print $2}' | sort -u)"
done

# ── 2. Every documented env var must reach a container ───────────────────────

if [ ! -f "${ENV_EXAMPLE}" ] || [ ! -f "${COMPOSE}" ]; then
  echo "ERROR: ${ENV_EXAMPLE} or ${COMPOSE} is missing." >&2
  exit 1
fi

while IFS= read -r raw_line; do
  line="${raw_line}"
  # Strip an optional leading comment marker so commented-out examples are
  # checked as well.
  case "${line}" in
    '#'*) line="${line#"${line%%[![:space:]]*}"}" ;;
  esac
  case "${line}" in
    [A-Za-z_]*) ;;
    *) continue ;;
  esac
  case "${line}" in
    *=*) ;;
    *) continue ;;
  esac
  key="${line%%=*}"
  case "${key}" in
    [A-Za-z_]* ) ;;
    * ) continue ;;
  esac
  if ! grep -qE '\$\{'"${key}"'([-:}]|$)' "${COMPOSE}"; then
    echo "ERROR: ${ENV_EXAMPLE} defines ${key} but ${COMPOSE} never reads it — it would never reach a container." >&2
    FAIL=1
  fi
done < "${ENV_EXAMPLE}"

if [ "${FAIL}" -ne 0 ]; then
  echo "" >&2
  echo "Documentation and code are out of sync (see above)." >&2
  exit 1
fi

echo "Documentation is consistent with the Makefile and the production compose file."
