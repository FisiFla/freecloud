# syntax=docker/dockerfile:1
# Multi-stage build for the Next.js dashboard using output: "standalone".
# Build context is the frontend/ directory.

FROM node:26-alpine@sha256:9bd229b644e391df00fb989efa732335aefc6170a05d8c163c5c8ca9870da401 AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

FROM node:26-alpine@sha256:9bd229b644e391df00fb989efa732335aefc6170a05d8c163c5c8ca9870da401 AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . ./
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM node:26-alpine@sha256:9bd229b644e391df00fb989efa732335aefc6170a05d8c163c5c8ca9870da401 AS runtime
WORKDIR /app
# Build identity for /api/version — server-only (never NEXT_PUBLIC_*).
ARG FREECLOUD_VERSION=dev
ARG GIT_SHA=unknown
ENV FREECLOUD_VERSION=$FREECLOUD_VERSION GIT_SHA=$GIT_SHA
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000
# The standalone output bundles a minimal node_modules + server.js.
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static
USER node
EXPOSE 3000
# Alpine's busybox wget is already in the image; /api/health is process
# liveness only, so this never depends on the backend.
HEALTHCHECK --interval=30s --timeout=3s --start-period=20s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:3000/api/health || exit 1
CMD ["node", "server.js"]
