# Multi-stage build for kycalendar. The web UI is the committed web/dist that CI rebuilds and
# diff-checks, so the image embeds exactly the verified bundle; there is no frontend stage.
# Base images are pinned by digest; Dependabot (docker ecosystem) proposes the bumps.

FROM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
# go.mod replaces go-webdav with this vendored fork; it must exist before download.
COPY third_party/ ./third_party/
RUN go mod download
COPY . ./
# The release without its "v" (the release workflow, ci.yml and `make docker-build` pass it);
# left empty, the binary reports "dev". Declared after the copy so a new version reuses the
# download layer.
ARG VERSION=
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X github.com/Busnes-app/kycalendar/internal/version.Version=${VERSION}" -o kycalendar ./cmd/server

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /app/kycalendar /app/kycalendar
# /app/backups is the optional mount for sealed local capsules; KYCALENDAR_BACKUP_DIR is set
# by the operator (compose does), so an image run bare keeps no local copies.
# UID/GID 10001 owns both; a bind-mounted host directory must be owned by it too (README).
RUN mkdir -p /app/data /app/backups && chown 10001:10001 /app/data /app/backups && chmod 700 /app/data /app/backups
USER 10001:10001

ENV KY_PORT=8080
# A container must listen on every interface; publish the port on loopback (compose does).
ENV KY_HOST=0.0.0.0
ENV KY_DATA_DIR=/app/data

EXPOSE 8080
VOLUME ["/app/data", "/app/backups"]

ENTRYPOINT ["/app/kycalendar"]
