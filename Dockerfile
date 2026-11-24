# Multi-stage build for kycalendar. The web UI is the committed web/dist that CI rebuilds and
# diff-checks, so the image embeds exactly the verified bundle; there is no frontend stage.
# Base images are pinned by digest; Dependabot (docker ecosystem) proposes the bumps.

FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
# go.mod replaces go-webdav with this vendored fork; it must exist before download.
COPY third_party/ ./third_party/
RUN go mod download
COPY . ./
# The release without its "v", X.Y.Z or X.Y.Z.N (the release workflow, ci.yml and
# `make docker-build` pass it); left empty, the binary reports internal/appversion/VERSION. Declared after the copy so a new version reuses the
# download layer.
# -trimpath keeps the build directory out of the binary, so the same commit, Go version and
# VERSION give the same bytes wherever it is built (docs/VERIFYING-RELEASES.md).
ARG VERSION=
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X github.com/Busnes-app/kycalendar/internal/appversion.release=${VERSION}" -o kycalendar ./cmd/server

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
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
