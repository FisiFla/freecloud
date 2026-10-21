#!/bin/sh
# FreeCloud Keycloak entrypoint.
#
# Keycloak has no <VAR>_FILE convention of its own, so the Docker secrets
# mounted into the container (as /run/secrets/<name>) are read here and
# exported under the environment variable names Keycloak expects. That keeps
# the secret values out of `docker inspect` and /proc/<pid>/environ.
#
# Missing files are simply skipped, so the same image still works in the
# localhost / e2e stacks where the values are plain environment variables.

set -e

# NOTE: the file-path variables are deliberately NOT prefixed with "KC_".
# Keycloak's kc.sh turns every KC_* environment variable into a CLI option
# (KC_DB_PASSWORD -> --db-password), so an unknown KC_ variable would make
# Keycloak refuse to start with "Unknown option".

read_secret() {
	# $1 = environment variable to export, $2 = path to the secret file
	if [ -n "$2" ] && [ -r "$2" ]; then
		value=$(cat "$2")
		export "$1=$value"
	fi
}

read_secret KEYCLOAK_ADMIN_PASSWORD "${FREECLOUD_KEYCLOAK_ADMIN_PASSWORD_FILE:-}"
read_secret KC_DB_PASSWORD "${FREECLOUD_KEYCLOAK_DB_PASSWORD_FILE:-}"
read_secret ACCESS_EVAL_TOKEN "${FREECLOUD_ACCESS_EVAL_TOKEN_FILE:-}"

exec /opt/keycloak/bin/kc.sh "$@"
