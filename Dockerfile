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

RUN apk add --no-cache ca-certificates tzdata su-exec && update-ca-certificates

WORKDIR /app
COPY --from=build /out/scdn-io-proxy /app/scdn-io-proxy
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

# 默认使用 1000:1000，方便与大多数 Linux 宿主机用户 UID/GID 对齐，避免挂载卷写入权限问题。
RUN addgroup -S -g 1000 app && adduser -S -u 1000 -G app app \
  && mkdir -p /data \
  && chown -R app:app /data \
  && chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 1080 8080

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
