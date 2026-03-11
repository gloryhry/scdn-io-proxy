package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"

	"scdn-io-proxy/internal/netx"
	"scdn-io-proxy/internal/pool"
)

func handleSOCKS5(ctx context.Context, c *netx.BufferedConn, upstream *pool.Upstream, user, pass string) {
	// greeting: VER, NMETHODS, METHODS...
	ver, err := readByte(c)
	if err != nil || ver != 0x05 {
		return
	}
	nMethods, err := readByte(c)
	if err != nil {
		return
	}
	methods := make([]byte, int(nMethods))
	if _, err := io.ReadFull(c, methods); err != nil {
		return
	}

	const userPassMethod = 0x02
	if !containsByte(methods, userPassMethod) {
		_, _ = c.Write([]byte{0x05, 0xFF})
		return
	}
	_, _ = c.Write([]byte{0x05, userPassMethod})

	// username/password auth: VER=0x01, ULEN, UNAME, PLEN, PASSWD
	aver, err := readByte(c)
	if err != nil || aver != 0x01 {
		return
	}
	ulen, err := readByte(c)
	if err != nil {
		return
	}
	uname := make([]byte, int(ulen))
	if _, err := io.ReadFull(c, uname); err != nil {
		return
	}
	plen, err := readByte(c)
	if err != nil {
		return
	}
	pwd := make([]byte, int(plen))
	if _, err := io.ReadFull(c, pwd); err != nil {
		return
	}

	if !constTimeEqual(string(uname), user) || !constTimeEqual(string(pwd), pass) {
		_, _ = c.Write([]byte{0x01, 0x01})
		return
	}
	_, _ = c.Write([]byte{0x01, 0x00})

	// request: VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT
	rver, err := readByte(c)
	if err != nil || rver != 0x05 {
		return
	}
	cmd, err := readByte(c)
	if err != nil {
		return
	}
	_, err = readByte(c) // RSV
	if err != nil {
		return
	}
	atyp, err := readByte(c)
	if err != nil {
		return
	}

	host, err := readSocksAddr(c, atyp)
	if err != nil {
		_ = writeSocksReply(c, 0x08) // Address type not supported
		return
	}
	port, err := readUint16(c)
	if err != nil {
		return
	}
	dest := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	if cmd != 0x01 {
		_ = writeSocksReply(c, 0x07) // Command not supported
		return
	}
	if upstream == nil {
		_ = writeSocksReply(c, 0x01)
		return
	}

	dialTimeout := 15 * time.Second
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	remote, err := pool.DialThroughUpstream(dctx, *upstream, "tcp", dest, dialTimeout)
	cancel()
	if err != nil {
		_ = writeSocksReply(c, 0x01)
		return
	}
	defer remote.Close()

	_ = writeSocksReply(c, 0x00)
	netx.BiCopy(c, remote)
}

func writeSocksReply(c net.Conn, rep byte) error {
	// 这里返回 0.0.0.0:0，够用。
	_, err := c.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
}

func readSocksAddr(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01: // IPv4
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return net.IP(b[:]).String(), nil
	case 0x03: // Domain
		lb := make([]byte, 1)
		if _, err := io.ReadFull(r, lb); err != nil {
			return "", err
		}
		l := int(lb[0])
		b := make([]byte, l)
		if _, err := io.ReadFull(r, b); err != nil {
			return "", err
		}
		return string(b), nil
	case 0x04: // IPv6
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", err
		}
		return net.IP(b[:]).String(), nil
	default:
		return "", fmt.Errorf("atyp=%d", atyp)
	}
}

func readUint16(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func readByte(r io.Reader) (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	return b[0], err
}

func containsByte(bs []byte, v byte) bool {
	for _, b := range bs {
		if b == v {
			return true
		}
	}
	return false
}

func constTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
