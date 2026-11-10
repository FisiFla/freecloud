#!/usr/bin/env bash
# Guard the localhost stack's first-boot secrets ordering.
#
# `secrets-init` generates .secrets/secrets.env, which services load via
# env_file. Compose resolves env_file when it CREATES a container, and a
# fresh `up` creates containers before their dependencies finish starting —
# so depends_on edges alone are NOT enough: a first boot created the
# frontend before the file existed and it ran with an empty AUTH_SECRET
# (sign-in 500s; found by the browser-e2e fresh-boot job, 2026-11).
# Two independent safeguards, both asserted here:
#   1. every env_file consumer also waits for secrets-init to complete
#      (start-order, and correctness for anything committing at start)
#   2. `make localhost-up` runs secrets-init to completion BEFORE `up`,
#      so the file exists before any container is created
# Usage: scripts/check-localhost-secrets.sh [compose-file] [makefile]
set -euo pipefail

python3 - "${1:-docker/docker-compose.localhost.yml}" "${2:-Makefile}" <<'PY'
import re, sys

compose = open(sys.argv[1]).read()
makefile = open(sys.argv[2]).read()
failures = []

# Split the compose file into per-service blocks (2-space indented keys
# under `services:`); enough structure for these textual invariants.
blocks = {}
current = None
for line in compose.splitlines():
    m = re.match(r"^  ([a-z][a-z0-9-]*):$", line)
    if m:
        current = m.group(1)
        blocks[current] = []
    elif current:
        blocks[current].append(line)
blocks = {k: "\n".join(v) for k, v in blocks.items()}

for name, body in blocks.items():
    if "secrets.env" not in body:
        continue
    if not re.search(r"secrets-init:\s*\n\s*condition:\s*service_completed_successfully", body):
        failures.append(
            f"service {name} loads .secrets/secrets.env via env_file but does not "
            "depends_on secrets-init (service_completed_successfully)"
        )

# Makefile: inside the localhost-up recipe, a secrets-init run must appear
# before the stack `up`.
m = re.search(r"^localhost-up:\n((?:\t.*\n)+)", makefile, re.M)
recipe = m.group(1) if m else ""
init_at = recipe.find("secrets-init")
up_at = recipe.find(" up")
if init_at == -1 or up_at == -1 or init_at > up_at:
    failures.append(
        "make localhost-up must run secrets-init to completion before `up` "
        "(env_file is resolved at container-create time, before depends_on completes)"
    )

for f in failures:
    print(f"check-localhost-secrets: {f}")
sys.exit(1 if failures else 0)
PY
