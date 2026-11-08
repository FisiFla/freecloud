# golang 1.27.3-alpine
FROM golang:1.27.3-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS builder
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o fleetdm-mock main.go

# alpine 3.20
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
# Run as a non-root user: the mock needs no privileges at all.
RUN addgroup -S mock && adduser -S -G mock mock
COPY --from=builder /app/fleetdm-mock /usr/local/bin/fleetdm-mock
USER mock
EXPOSE 8080
CMD ["fleetdm-mock"]
