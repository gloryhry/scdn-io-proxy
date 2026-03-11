package proxy

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"

	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/netx"
	"scdn-io-proxy/internal/pool"
)

type Server struct {
	ListenAddr string
	Rotator    *Rotator
	Cfg        *config.Manager
	Log        *log.Logger
}

func (s *Server) Serve(ctx context.Context) error {
	if s.Log == nil {
		s.Log = log.Default()
	}
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return err
	}
	return s.ServeListener(ctx, ln)
}

func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	if s.Log == nil {
		s.Log = log.Default()
	}
	s.Log.Printf("[proxy] listening on %s", ln.Addr().String())

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			s.Log.Printf("[proxy] accept error: %v", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// 每个客户端连接固定使用一个上游出口，直到断开。
	snap := s.Rotator.Snapshot()
	var upstream *pool.Upstream
	if snap.HasProxy {
		u := pool.FromStoreProxy(snap.Proxy)
		upstream = &u
	}

	br := bufio.NewReader(conn)
	b, err := br.Peek(1)
	if err != nil {
		return
	}
	bc := &netx.BufferedConn{Conn: conn, R: br}

	set := s.Cfg.Get()
	user := set.ProxyAuthUser
	pass := set.ProxyAuthPass

	if b[0] == 0x05 {
		handleSOCKS5(ctx, bc, upstream, user, pass)
		return
	}
	handleHTTP(ctx, bc, upstream, user, pass)
}
