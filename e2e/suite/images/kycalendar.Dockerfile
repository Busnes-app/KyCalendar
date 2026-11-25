# kye2e/kycalendar:<sha>: KyCalendar at a pinned commit, built with the kye2e tag. Never pushed.
FROM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG REPO
ARG SHA
ADD ${REPO}#${SHA} /src
WORKDIR /src
RUN CGO_ENABLED=0 go build -trimpath -tags kye2e -o /kycalendar ./cmd/server

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk --no-cache add ca-certificates && mkdir -p /app/data && chown 10001:10001 /app/data && chmod 700 /app/data
COPY --from=build /kycalendar /app/kycalendar
USER 10001:10001
ENV KY_PORT=8080 KY_HOST=0.0.0.0 KY_DATA_DIR=/app/data
ENTRYPOINT ["/app/kycalendar"]
