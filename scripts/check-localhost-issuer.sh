#!/usr/bin/env bash
# Guard the localhost stack's split-horizon OIDC invariant.
#
# Browser sign-in on docker-compose.localhost.yml silently broke three
# ways at once because three different views of "the Keycloak URL"
# drifted apart (found 2026-11-09):
#   1. tokens carry iss from KC_HOSTNAME (public, browser-reachable)
#   2. the frontend's server-side OIDC calls must use an INTERNAL
#      origin (AUTH_KEYCLOAK_INTERNAL_URL) — the public one is
#      unreachable from inside the container
#   3. the backend must expect the PUBLIC iss (KEYCLOAK_ISSUER_URL)
#      while fetching JWKS over the internal KEYCLOAK_URL
# This script asserts all three legs agree. Usage:
#   scripts/check-localhost-issuer.sh [compose-file]
set -euo pipefail

COMPOSE_FILE="${1:-docker/docker-compose.localhost.yml}"

docker compose -f "$COMPOSE_FILE" config --format json | python3 -c '
import json, sys
from urllib.parse import urlparse

cfg = json.load(sys.stdin)
services = cfg.get("services", {})

def env(name):
    e = services.get(name, {}).get("environment", {})
    if isinstance(e, list):
        e = dict(item.split("=", 1) for item in e if "=" in item)
    return e

def origin(url):
    p = urlparse(url)
    return f"{p.scheme}://{p.netloc}" if p.scheme and p.netloc else ""

failures = []

kc = env("keycloak")
hostname = kc.get("KC_HOSTNAME", "")
port = kc.get("KC_HOSTNAME_PORT", "")
if hostname.startswith("http://") or hostname.startswith("https://"):
    public = origin(hostname)
else:
    public = f"http://{hostname}:{port}" if hostname and port else ""
    # The v1 hostname+port form leaves the discovery issuer
    # request-derived in start-dev: fetched over the internal URL it
    # comes back with the internal port and Auth.js rejects sign-in
    # with an issuer mismatch. URL form pins it.
    failures.append("keycloak KC_HOSTNAME must be URL-form (e.g. http://localhost:8081), not hostname+KC_HOSTNAME_PORT: the v1 form makes the discovery issuer request-derived")
if not public:
    failures.append("keycloak KC_HOSTNAME/KC_HOSTNAME_PORT do not define a public origin")

fe = env("frontend")
issuer = fe.get("AUTH_KEYCLOAK_ISSUER", "")
if public and origin(issuer) != public:
    failures.append(f"frontend AUTH_KEYCLOAK_ISSUER origin {origin(issuer)!r} != Keycloak public origin {public!r} (browser authorize/iss mismatch)")
if not fe.get("AUTH_KEYCLOAK_INTERNAL_URL"):
    failures.append("frontend AUTH_KEYCLOAK_INTERNAL_URL is unset: server-side OIDC calls will target the public issuer, unreachable from inside the container")

be = env("backend")
issuer_url = be.get("KEYCLOAK_ISSUER_URL", "")
backend_url = be.get("KEYCLOAK_URL", "")
if public and origin(issuer_url) != public and origin(backend_url) != public:
    failures.append(f"backend expects iss from {origin(backend_url)!r} but tokens carry {public!r}: set KEYCLOAK_ISSUER_URL to the public origin")

if failures:
    print("localhost issuer invariant VIOLATED:")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print(f"localhost issuer invariant OK (public origin {public})")
'
