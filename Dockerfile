FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine3.24 AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETARCH
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build \
    -trimpath \
    -ldflags="-s -w -X github.com/lobanovsky/trip-pip-backend/internal/buildinfo.Commit=$VERSION" \
    -o /out/trip-pip-backend \
    ./cmd/api

FROM alpine:3.24.1

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=build /out/trip-pip-backend ./trip-pip-backend

USER 65532:65532
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --quiet --spider http://127.0.0.1:8080/api/v1/ping || exit 1

ENTRYPOINT ["/app/trip-pip-backend"]
