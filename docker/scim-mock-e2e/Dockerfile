FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN CGO_ENABLED=0 go build -o scim-mock-e2e main.go

FROM alpine:3.20
COPY --from=builder /app/scim-mock-e2e /usr/local/bin/scim-mock-e2e
EXPOSE 8080
CMD ["scim-mock-e2e"]
