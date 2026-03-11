# scdn-io-proxy

基于 `proxy.scdn.io` 代理池的中转服务：

- 定时拉取代理（按协议、区域）
- 用测试 URL 验证可用性
- 可用代理入 SQLite（`protocol + ip + port` 唯一）
- 对外同端口同时提供 HTTP Proxy 与 SOCKS5（自动识别），两者均支持用户名密码认证
- 出口代理按保持时长轮询切换（仅影响新连接，已建立连接不受影响）
- 管理后台（Basic 认证）：监控当前出口、剩余保持时间、代理列表、参数设置
- 池内代理按间隔复测，失败自动移除

## 快速开始

```powershell
go run ./cmd/scdn-io-proxy `
  --listen "0.0.0.0:1080" `
  --admin "127.0.0.1:8080" `
  --db "proxy-pool.sqlite" `
  --scdn-api-key "YOUR_API_KEY" `
  --fetch-protocol "http" `
  --country "US" `
  --test-url "https://www.example.com/" `
  --proxy-user "proxy" `
  --proxy-pass "proxy"
```

管理后台：打开 `http://127.0.0.1:8080/`，使用 Basic 认证登录（默认与 `proxy-user/proxy-pass` 一致，可在设置页单独配置）。

注意：Chromium 内核浏览器（Chrome/Edge 等）通常会拦截 `6000` 端口（`ERR_UNSAFE_PORT`），请避免使用 `:6000` 作为管理后台端口。

## 使用示例

HTTP 代理（示例以 curl）：

```bash
curl -x "http://proxy:proxy@127.0.0.1:1080" "https://www.example.com/"
```

SOCKS5 代理：

```bash
curl --socks5 "proxy:proxy@127.0.0.1:1080" "https://www.example.com/"
```
