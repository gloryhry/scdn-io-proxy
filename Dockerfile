# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
  go build -trimpath -buildvcs=false -ldflags "-s -w" \
  -o /out/scdn-io-proxy ./cmd/scdn-io-proxy

FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && update-ca-certificates

WORKDIR /app
COPY --from=build /out/scdn-io-proxy /app/scdn-io-proxy

RUN addgroup -S app && adduser -S -G app app
USER app

EXPOSE 1080 8080

ENTRYPOINT ["/app/scdn-io-proxy"]
