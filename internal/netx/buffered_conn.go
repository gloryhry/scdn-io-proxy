package netx

import (
	"bufio"
	"net"
)

// BufferedConn 用于在 Peek/ReadResponse 等场景下保留 bufio.Reader 已缓存的数据。
type BufferedConn struct {
	net.Conn
	R *bufio.Reader
}

func (c *BufferedConn) Read(p []byte) (int, error) {
	return c.R.Read(p)
}
