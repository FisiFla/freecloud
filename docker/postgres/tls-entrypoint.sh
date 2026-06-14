#!/usr/bin/env sh
# Generate a self-signed server certificate (if absent) and start Postgres with
# TLS enabled, so the backend can connect with sslmode=require even on a private
# Docker network. The cert is for transport encryption only (clients use
# sslmode=require, not verify-full), so a self-signed cert is sufficient.
set -e

SSL_DIR=/var/lib/postgresql/ssl
CRT="$SSL_DIR/server.crt"
KEY="$SSL_DIR/server.key"

if [ ! -f "$CRT" ] || [ ! -f "$KEY" ]; then
	mkdir -p "$SSL_DIR"
	openssl req -new -x509 -days 3650 -nodes \
		-subj "/CN=postgres" \
		-keyout "$KEY" -out "$CRT" >/dev/null 2>&1
	chmod 600 "$KEY"
	chown postgres:postgres "$KEY" "$CRT"
fi

exec docker-entrypoint.sh postgres \
	-c ssl=on \
	-c ssl_cert_file="$CRT" \
	-c ssl_key_file="$KEY"
