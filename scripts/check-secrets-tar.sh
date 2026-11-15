#!/usr/bin/env bash
# check-secrets-tar.sh — refuse to extract a secrets.tar with unsafe members.
#
# Usage: scripts/check-secrets-tar.sh <secrets.tar>
#
# Exits 0 iff every member is a regular file or directory under the .secrets/
# prefix, with no "..", no absolute path and no symlink/hardlink/device
# entries. Run this BEFORE `tar -xf` on any bundle you did not take yourself
# (see docs/BACKUP_RESTORE.md "Restore from a bundle on a fresh host"):
#
#   scripts/check-secrets-tar.sh freecloud-bundle-<ts>/secrets.tar \
#     && tar -xf freecloud-bundle-<ts>/secrets.tar
#
# scripts/verify-bundle.sh runs the same check automatically; this script is
# the entry point for the runbook and for any manual extraction.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/backup-lib.sh
. "${SCRIPT_DIR}/backup-lib.sh"

if [ $# -ne 1 ] || [ "$1" = "-h" ] || [ "$1" = "--help" ]; then
  echo "Usage: scripts/check-secrets-tar.sh <secrets.tar>" >&2
  exit 1
fi

fc_check_secrets_tar "$1"
