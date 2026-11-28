# kye2e/kynotes:<sha>: KyNotes at a pinned commit, built with the kye2e tag. Never pushed.
# internal/web/dist is committed, so no frontend build; the runtime matches the app's own image.
FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
ARG REPO
ARG SHA
ADD ${REPO}#${SHA} /src
WORKDIR /src
RUN mkdir -p /data /tmp && chown 65532:65532 /data /tmp
RUN CGO_ENABLED=0 go build -trimpath -tags kye2e -o /kynotes-server ./cmd/kynotes-server

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /kynotes-server /kynotes-server
COPY --from=build --chown=nonroot:nonroot /data /data
COPY --from=build --chown=nonroot:nonroot /tmp /tmp
USER nonroot
ENTRYPOINT ["/kynotes-server"]
