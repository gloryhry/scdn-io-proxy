package pool

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func TestUpstream(ctx context.Context, u Upstream, testURL string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()

	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}

	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       30 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	switch u.Protocol {
	case "http", "https":
		// 使用标准的 HTTP Proxy 行为：HTTP 走 absolute-form，HTTPS 走 CONNECT。
		pu := &url.URL{Scheme: "http", Host: u.Addr()}
		transport.Proxy = http.ProxyURL(pu)
		transport.DialContext = d.DialContext
	case "socks5":
		transport.Proxy = nil
		transport.DialContext = func(c context.Context, network, addr string) (net.Conn, error) {
			return DialThroughUpstream(c, u, network, addr, timeout)
		}
	default:
		return 0, fmt.Errorf("不支持的上游协议: %s", u.Protocol)
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 只要能连通并拿到首包即可，不跟随 301/302 以免被跳转到 HTTPS 导致误判。
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "scdn-io-proxy/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// 读一点点就够，避免把大文件当成“可用性测试”。
	_, _ = io.CopyN(io.Discard, resp.Body, 32<<10)

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("测试URL返回非2xx/3xx: %s", resp.Status)
	}

	return time.Since(start), nil
}
