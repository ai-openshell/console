# Multi-stage build: frontend static assets + Go BFF in one image.
# The BFF serves the frontend from STATIC_DIR — one container, one process.

# --- Frontend build ---
FROM node:22-alpine AS frontend
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY frontend/ ./
# The commit this image is built from, for the About dialog, which shows its
# first seven characters: the same ones as in the image's sha-<7> tag. CI
# passes it. Without it the build still works and the dialog shows no commit,
# which is true of an image nobody told where it came from; the gateway
# release line it shows comes from the BFF and needs nothing passed.
#
# Declared here, after everything else in the stage, so that a new commit
# re-runs the build below and not the install above.
ARG DASHBOARD_COMMIT=""
RUN npm run build

# --- Backend build ---
FROM golang:1.26.7-alpine AS backend
WORKDIR /app/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

# --- Runtime ---
FROM alpine:3.20
RUN adduser -D -u 1001 dashboard
USER 1001
COPY --from=backend /out/server /usr/local/bin/openshell-dashboard
COPY --from=frontend /app/frontend/dist /srv/static
ENV PORT=8080 \
    STATIC_DIR=/srv/static
EXPOSE 8080

# Which OpenShell gateway release line this build is for, and the SDK it was
# built against (ADR 0005), as labels for someone holding only an image
# reference:
#   skopeo inspect docker://<image> | jq .Labels
#
# These two build args set the labels and nothing else. The BFF does not read
# them: the release line it compares a gateway with is compiled into the
# binary (backend/pkg/models/gateway_release_line.go), so an image built with
# no build args and started with no environment still knows it. Do not add an
# ENV for the line here; GATEWAY_RELEASE_LINE is an override for tests and
# local development.
#
# CI passes them from `node scripts/gateway-range.mjs`, which derives them from
# the release deploy/ci/gateway-pins.json names — the same source as
# the release notes, so the two cannot disagree. A plain `docker build` without
# them still works and leaves both labels empty. Because empty is allowed,
# ci.yml's image-range job reads the pushed image back and fails if CI's own
# build came out empty.
#
# Kept at the end of the stage so that changing them rebuilds nothing above.
ARG GATEWAY_RELEASE_LINE=""
ARG OPENSHELL_SDK_VERSION=""
LABEL io.github.ai-openshell.console.gateway.line="${GATEWAY_RELEASE_LINE}" \
      io.github.ai-openshell.console.sdk="${OPENSHELL_SDK_VERSION}"

ENTRYPOINT ["/usr/local/bin/openshell-dashboard"]
