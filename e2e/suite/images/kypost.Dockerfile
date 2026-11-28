# check=skip=InvalidDefaultArgInFrom
# The harness must supply RELEASE_SHA256 from the local OCI export for each build.
# The release image keeps its entrypoint and supervisord config. Only kypost-server is replaced
# with a -tags kye2e build of the same pinned commit. Never pushed.
ARG RELEASE_SHA256
FROM golang:1.27.4@sha256:08bcb99a38f85a265f9a93d1d3323e2440eec333380f3ea26ee0dec9264bb41c AS build
ARG REPO
ARG SHA
ADD ${REPO}#${SHA} /src
WORKDIR /src/backend
RUN CGO_ENABLED=0 go build -trimpath -tags kye2e -o /kypost-server ./cmd/main.go

FROM release@sha256:${RELEASE_SHA256}
COPY --from=build /kypost-server /usr/local/bin/kypost-server
