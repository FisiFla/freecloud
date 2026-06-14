FROM postgres:16-alpine
RUN apk add --no-cache openssl
COPY tls-entrypoint.sh /usr/local/bin/tls-entrypoint.sh
RUN chmod +x /usr/local/bin/tls-entrypoint.sh
ENTRYPOINT ["tls-entrypoint.sh"]
