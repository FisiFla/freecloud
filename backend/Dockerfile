# syntax=docker/dockerfile:1
# Multi-stage build for the FreeCloud Go backend.
# Build context is the backend/ directory.

FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
# Static, stripped binary so it runs on a distroless/scratch base.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/freecloud-server ./cmd/server

# Distroless nonroot runtime: no shell, no package manager, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/freecloud-server /freecloud-server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/freecloud-server"]
