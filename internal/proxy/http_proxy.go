package proxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"scdn-io-proxy/internal/netx"
	"scdn-io-proxy/internal/pool"
)

func handleHTTP(ctx context.Context, c *netx.BufferedConn, upstream *pool.Upstream, user, pass string) {
	br := c.R
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}

		if !checkHTTPProxyAuth(req, user, pass) {
			_ = writeHTTP407(c)
			return
		}

		// CONNECT 通常用于 HTTPS，建立隧道后直接双向透传。
		if strings.EqualFold(req.Method, http.MethodConnect) {
			dest := normalizeHostPort(req.Host, 443)
			if req.Body != nil {
				_ = req.Body.Close()
			}
			if upstream == nil {
				_ = writeHTTPStatus(c, http.StatusServiceUnavailable, "No upstream proxy")
				return
			}

			dialTimeout := 15 * time.Second
			dctx, cancel := context.WithTimeout(ctx, dialTimeout)
			remote, err := pool.DialThroughUpstream(dctx, *upstream, "tcp", dest, dialTimeout)
			cancel()
			if err != nil {
				_ = writeHTTPStatus(c, http.StatusBadGateway, "Bad gateway")
				return
			}
			defer remote.Close()

			_, _ = fmt.Fprintf(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
			netx.BiCopy(c, remote)
			return
		}

		host := req.URL.Host
		scheme := req.URL.Scheme
		if host == "" {
			host = req.Host
		}
		if scheme == "" {
			scheme = "http"
		}
		defaultPort := 80
		if strings.EqualFold(scheme, "https") {
			defaultPort = 443
		}
		dest := normalizeHostPort(host, defaultPort)

		if upstream == nil {
			_ = writeHTTPStatus(c, http.StatusServiceUnavailable, "No upstream proxy")
			return
		}

		dialTimeout := 15 * time.Second
		var (
			remote  net.Conn
			dialErr error
			outReq  *http.Request
		)

		// 对于 HTTP 目标地址，如果上游也是 HTTP/HTTPS 代理，优先按标准代理链路转发（absolute-form）。
		// 这能兼容不支持 CONNECT 到 80 端口的上游。
		if strings.EqualFold(scheme, "http") && (upstream.Protocol == "http" || upstream.Protocol == "https") {
			d := &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
			dctx, cancel := context.WithTimeout(ctx, dialTimeout)
			remote, dialErr = d.DialContext(dctx, "tcp", upstream.Addr())
			cancel()
			if dialErr != nil {
				_ = writeHTTPStatus(c, http.StatusBadGateway, "Bad gateway")
				return
			}

			outReq = req.Clone(ctx)
			outReq.RequestURI = ""
			if outReq.URL != nil {
				outReq.URL.Scheme = scheme
				outReq.URL.Host = host
			}
			outReq.Header.Del("Proxy-Authorization")
			outReq.Header.Del("Proxy-Connection")

			if err := outReq.WriteProxy(remote); err != nil {
				_ = remote.Close()
				_ = writeHTTPStatus(c, http.StatusBadGateway, "Bad gateway")
				return
			}
		} else {
			dctx, cancel := context.WithTimeout(ctx, dialTimeout)
			remote, dialErr = pool.DialThroughUpstream(dctx, *upstream, "tcp", dest, dialTimeout)
			cancel()
			if dialErr != nil {
				_ = writeHTTPStatus(c, http.StatusBadGateway, "Bad gateway")
				return
			}

			// 转发给目标站点时使用 origin-form。
			outReq = req.Clone(ctx)
			outReq.RequestURI = ""
			if outReq.URL != nil {
				outReq.URL.Scheme = ""
				outReq.URL.Host = ""
			}
			outReq.Header.Del("Proxy-Authorization")
			outReq.Header.Del("Proxy-Connection")

			// 把请求写到远端。
			if err := outReq.Write(remote); err != nil {
				_ = remote.Close()
				_ = writeHTTPStatus(c, http.StatusBadGateway, "Bad gateway")
				return
			}
		}

		if req.Body != nil {
			_ = req.Body.Close()
		}

		// 读远端响应并写回客户端。
		rbr := bufio.NewReader(remote)
		resp, err := http.ReadResponse(rbr, outReq)
		if err != nil {
			_ = remote.Close()
			_ = writeHTTPStatus(c, http.StatusBadGateway, "Bad gateway")
			return
		}
		_ = resp.Write(c)
		_ = resp.Body.Close()
		_ = remote.Close()

		if req.Close || resp.Close {
			return
		}
	}
}

func checkHTTPProxyAuth(req *http.Request, user, pass string) bool {
	h := strings.TrimSpace(req.Header.Get("Proxy-Authorization"))
	if h == "" {
		return false
	}

	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return false
	}

	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return false
	}

	expect := user + ":" + pass
	if len(raw) != len(expect) {
		return false
	}
	return subtle.ConstantTimeCompare(raw, []byte(expect)) == 1
}

func writeHTTP407(c net.Conn) error {
	_, err := fmt.Fprintf(c,
		"HTTP/1.1 407 Proxy Authentication Required\r\n"+
			"Proxy-Authenticate: Basic realm=\"scdn-io-proxy\"\r\n"+
			"Content-Length: 0\r\n"+
			"Connection: close\r\n\r\n")
	return err
}

func writeHTTPStatus(c net.Conn, code int, msg string) error {
	body := msg + "\n"
	_, err := fmt.Fprintf(c,
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
	return err
}

func normalizeHostPort(hostport string, defaultPort int) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if host, port, err := net.SplitHostPort(hostport); err == nil {
		return net.JoinHostPort(strings.Trim(host, "[]"), port)
	}

	// 没有端口的情况：host（IPv4/域名）或未加括号的IPv6。
	if strings.Count(hostport, ":") == 1 {
		i := strings.LastIndexByte(hostport, ':')
		host := strings.Trim(hostport[:i], "[]")
		port := hostport[i+1:]
		if port != "" && isAllDigits(port) {
			return net.JoinHostPort(host, port)
		}
	}
	host := strings.Trim(hostport, "[]")
	return net.JoinHostPort(host, fmt.Sprintf("%d", defaultPort))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
