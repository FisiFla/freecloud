# golang 1.26.10-alpine
FROM golang:1.26.10-alpine@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS builder
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o scim-mock-e2e main.go

# alpine 3.20
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
# Run as a non-root user: the mock needs no privileges at all.
RUN addgroup -S mock && adduser -S -G mock mock
COPY --from=builder /app/scim-mock-e2e /usr/local/bin/scim-mock-e2e
USER mock
EXPOSE 8080
CMD ["scim-mock-e2e"]
