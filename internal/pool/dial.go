package pool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"

	"scdn-io-proxy/internal/netx"
)

func DialThroughUpstream(ctx context.Context, u Upstream, network, addr string, timeout time.Duration) (net.Conn, error) {
	switch u.Protocol {
	case "http", "https":
		return dialThroughHTTPProxy(ctx, u.Addr(), addr, timeout)
	case "socks5":
		return dialThroughSOCKS5(ctx, u.Addr(), network, addr, timeout)
	default:
		return nil, fmt.Errorf("不支持的上游协议: %s", u.Protocol)
	}
}

func dialThroughHTTPProxy(ctx context.Context, proxyAddr, destAddr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}

	// 通过 HTTP CONNECT 建立到目标的 TCP 隧道。
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: keep-alive\r\n\r\n", destAddr, destAddr); err != nil {
		_ = conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("上游HTTP代理 CONNECT 失败: %s", resp.Status)
	}

	return &netx.BufferedConn{Conn: conn, R: br}, nil
}

func dialThroughSOCKS5(ctx context.Context, proxyAddr, network, addr string, timeout time.Duration) (net.Conn, error) {
	base := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	d, err := proxy.SOCKS5("tcp", proxyAddr, nil, base)
	if err != nil {
		return nil, err
	}

	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, e := d.Dial(network, addr)
		ch <- result{c: c, err: e}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.c, r.err
	}
}
