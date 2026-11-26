# kye2e/kypost:<sha>: KyPost's own release image at the pinned commit (the build context named
# release, built from its Dockerfile) with only kypost-server replaced by a -tags kye2e build of
# the same commit. Everything else, entrypoint and supervisord included, is the release's. Never
# pushed.
FROM golang:1.27.4@sha256:08bcb99a38f85a265f9a93d1d3323e2440eec333380f3ea26ee0dec9264bb41c AS build
ARG REPO
ARG SHA
ADD ${REPO}#${SHA} /src
WORKDIR /src/backend
RUN CGO_ENABLED=0 go build -trimpath -tags kye2e -o /kypost-server ./cmd/main.go

FROM release
COPY --from=build /kypost-server /usr/local/bin/kypost-server
