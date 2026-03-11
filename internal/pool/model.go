package pool

import (
	"fmt"

	"scdn-io-proxy/internal/store"
)

type Upstream struct {
	Protocol    string
	CountryCode string
	IP          string
	Port        int
}

func (u Upstream) Addr() string {
	return fmt.Sprintf("%s:%d", u.IP, u.Port)
}

func FromStoreProxy(p store.Proxy) Upstream {
	return Upstream{
		Protocol:    p.Protocol,
		CountryCode: p.CountryCode,
		IP:          p.IP,
		Port:        p.Port,
	}
}
