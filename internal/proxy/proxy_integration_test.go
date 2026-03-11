package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/netx"
	"scdn-io-proxy/internal/store"
)

func TestProxyServer_HTTPAndSOCKS5_OnSamePort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gotReq := make(chan struct{}, 10)

	// 目的站点：简单返回 200。
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotReq <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer dst.Close()

	u, _ := url.Parse(dst.URL)
	destAddr := u.Host

	// 上游：本地 HTTP CONNECT 代理（无认证）。
	upAddr, upClose := startTestHTTPConnectProxy(t)
	defer upClose()

	t.Run("upstream-sanity", func(t *testing.T) {
		c, err := net.DialTimeout("tcp", upAddr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial upstream: %v", err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))

		if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", destAddr, destAddr); err != nil {
			t.Fatalf("write CONNECT: %v", err)
		}
		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatalf("read connect resp: %v", err)
		}
		// CONNECT 响应后连接立刻变为隧道；不要对 Body 做任何 Close/Drain 操作，避免破坏隧道语义。
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("connect status=%d", resp.StatusCode)
		}

		if _, err := fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", destAddr); err != nil {
			t.Fatalf("write GET: %v", err)
		}
		select {
		case <-gotReq:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("destination 没收到请求（上游隧道未透传）")
		}
		r2, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		b, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if r2.StatusCode != http.StatusOK || string(b) != "ok" {
			t.Fatalf("unexpected resp: %s body=%q", r2.Status, string(b))
		}
	})

	// SQLite：临时文件。
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	initial := config.DefaultSettings()
	initial.FetchProtocol = "http"
	initial.CountryCode = "all"
	initial.ProxyAuthUser = "u"
	initial.ProxyAuthPass = "p"
	initial.AdminAuthUser = "u"
	initial.AdminAuthPass = "p"
	if err := initial.Validate(); err != nil {
		t.Fatalf("settings validate: %v", err)
	}
	cfgMgr, err := config.NewManager(ctx, st, initial)
	if err != nil {
		t.Fatalf("config.NewManager: %v", err)
	}

	upHost, upPortStr, _ := net.SplitHostPort(upAddr)
	upPort, _ := parsePort(upPortStr)
	if err := st.UpsertUsableProxy(ctx, store.Proxy{
		Protocol:    "http",
		CountryCode: "all",
		IP:          upHost,
		Port:        upPort,
	}, time.Now(), 10*time.Millisecond); err != nil {
		t.Fatalf("UpsertUsableProxy: %v", err)
	}

	rot := NewRotator(st)
	if err := rot.Advance(ctx, 10*time.Second); err != nil {
		t.Fatalf("rot.Advance: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	proxyAddr := ln.Addr().String()

	srv := &Server{
		Rotator: rot,
		Cfg:     cfgMgr,
		Log:     logDiscard(),
	}
	go func() { _ = srv.ServeListener(ctx, ln) }()

	t.Run("http-connect", func(t *testing.T) {
		c, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))

		auth := base64.StdEncoding.EncodeToString([]byte("u:p"))
		if _, err := fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", destAddr, destAddr, auth); err != nil {
			t.Fatalf("write CONNECT: %v", err)
		}

		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
		if err != nil {
			t.Fatalf("read connect resp: %v", err)
		}
		// CONNECT 响应后连接立刻变为隧道；不要对 Body 做任何 Close/Drain 操作，避免破坏隧道语义。
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("connect status=%d", resp.StatusCode)
		}

		if _, err := fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", destAddr); err != nil {
			t.Fatalf("write GET: %v", err)
		}
		select {
		case <-gotReq:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("destination 没收到请求（中转隧道未透传）")
		}
		r2, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		b, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if r2.StatusCode != http.StatusOK || string(b) != "ok" {
			t.Fatalf("unexpected resp: %s body=%q", r2.Status, string(b))
		}
	})

	t.Run("socks5", func(t *testing.T) {
		d, err := proxy.SOCKS5("tcp", proxyAddr, &proxy.Auth{User: "u", Password: "p"}, &net.Dialer{Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("SOCKS5 dialer: %v", err)
		}
		c, err := d.Dial("tcp", destAddr)
		if err != nil {
			t.Fatalf("dial via socks5: %v", err)
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))

		fmt.Fprintf(c, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", destAddr)
		select {
		case <-gotReq:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("destination 没收到请求（SOCKS5未透传）")
		}
		br := bufio.NewReader(c)
		resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
		if err != nil {
			t.Fatalf("read resp: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(b) != "ok" {
			t.Fatalf("unexpected resp: %s body=%q", resp.Status, string(b))
		}
	})
}

func startTestHTTPConnectProxy(t *testing.T) (addr string, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleUpstreamConn(c)
		}
	}()

	return ln.Addr().String(), func() { cancel() }
}

func handleUpstreamConn(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method != http.MethodConnect {
		_, _ = io.WriteString(c, "HTTP/1.1 405 Method Not Allowed\r\nContent-Length: 0\r\n\r\n")
		return
	}
	dest := req.Host
	_ = req.Body.Close()

	remote, err := net.DialTimeout("tcp", dest, 5*time.Second)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer remote.Close()

	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	// br 里可能有缓存的后续字节，必须保留。
	netx.BiCopy(&netx.BufferedConn{Conn: c, R: br}, remote)
}

func parsePort(s string) (int, error) {
	var p int
	_, err := fmt.Sscanf(s, "%d", &p)
	return p, err
}

func logDiscard() *log.Logger {
	return log.New(io.Discard, "", 0)
}
