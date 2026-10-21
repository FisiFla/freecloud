FROM postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea  # postgres 16-alpine
RUN apk add --no-cache openssl
COPY tls-entrypoint.sh /usr/local/bin/tls-entrypoint.sh
RUN chmod +x /usr/local/bin/tls-entrypoint.sh
ENTRYPOINT ["tls-entrypoint.sh"]
