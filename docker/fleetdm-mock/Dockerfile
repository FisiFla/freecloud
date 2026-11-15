# golang 1.27.4-alpine
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o fleetdm-mock main.go

# alpine 3.20
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
# Run as a non-root user: the mock needs no privileges at all.
RUN addgroup -S mock && adduser -S -G mock mock
COPY --from=builder /app/fleetdm-mock /usr/local/bin/fleetdm-mock
USER mock
EXPOSE 8080
CMD ["fleetdm-mock"]
