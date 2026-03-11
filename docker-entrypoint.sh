#!/bin/sh
set -eu

APP_USER="${APP_USER:-app}"
APP_GROUP="${APP_GROUP:-app}"

mkdir -p "/data"

if [ "$(id -u)" = "0" ]; then
  # 绑定挂载卷在首次启动时常由 root 创建，导致非 root 用户无法写入 SQLite（含 -wal/-shm/-journal）。
  # 这里尽量修复 /data 归属，并在仍不可写时给出明确报错。
  chown -R "${APP_USER}:${APP_GROUP}" "/data" 2>/dev/null || true

  if ! su-exec "${APP_USER}:${APP_GROUP}" sh -c 'touch "/data/.scdn_write_test" 2>/dev/null'; then
    echo "错误: /data 目录不可写，SQLite 无法创建/写入数据库文件。" >&2
    echo "请检查 docker-compose 的 volumes 挂载与宿主机目录权限（例如: sudo chown -R 1000:1000 ./data）。" >&2
    ls -ld "/data" >&2 || true
    exit 1
  fi
  rm -f "/data/.scdn_write_test" || true

  exec su-exec "${APP_USER}:${APP_GROUP}" "/app/scdn-io-proxy" "$@"
fi

exec "/app/scdn-io-proxy" "$@"

