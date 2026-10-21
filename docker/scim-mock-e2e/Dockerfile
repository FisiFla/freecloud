FROM golang:1.22-alpine@sha256:1699c10032ca2582ec89a24a1312d986a3f094aed3d5c1147b19880afe40e052 AS builder  # golang 1.22-alpine
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o scim-mock-e2e main.go

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc  # alpine 3.20
# Run as a non-root user: the mock needs no privileges at all.
RUN addgroup -S mock && adduser -S -G mock mock
COPY --from=builder /app/scim-mock-e2e /usr/local/bin/scim-mock-e2e
USER mock
EXPOSE 8080
CMD ["scim-mock-e2e"]
