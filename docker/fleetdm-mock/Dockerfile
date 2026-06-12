FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o fleetdm-mock main.go

FROM alpine:3.20
COPY --from=builder /app/fleetdm-mock /usr/local/bin/fleetdm-mock
EXPOSE 8080
CMD ["fleetdm-mock"]
