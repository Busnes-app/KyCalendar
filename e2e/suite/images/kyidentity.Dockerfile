# kye2e/kyidentity:<sha>: KyIdentity at a pinned commit, built with the kye2e tag. Never pushed.
# web/dist is committed, so no frontend build; css/ and fonts/ ship as in the app's own image.
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG REPO
ARG SHA
ADD ${REPO}#${SHA} /src
WORKDIR /src
RUN CGO_ENABLED=0 go build -trimpath -tags kye2e -o /kyidentity ./cmd/kyidentity

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk --no-cache add ca-certificates tzdata && mkdir -p /data && chown 10001:10001 /data && chmod 700 /data
COPY --from=build /src/css /css
COPY --from=build /src/fonts /fonts
COPY --from=build /kyidentity /usr/local/bin/kyidentity
USER 10001:10001
ENV KYIDENTITY_DATA_DIR=/data
ENTRYPOINT ["/usr/local/bin/kyidentity"]
