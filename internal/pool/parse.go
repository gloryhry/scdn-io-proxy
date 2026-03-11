package pool

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

func ParseIPPort(v string) (string, int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", 0, fmt.Errorf("空地址")
	}

	host, portStr, err := net.SplitHostPort(v)
	if err != nil {
		// 兼容某些返回值不严格的情况：用最后一个冒号分割。
		i := strings.LastIndexByte(v, ':')
		if i <= 0 || i >= len(v)-1 {
			return "", 0, fmt.Errorf("解析地址失败: %s", v)
		}
		host = v[:i]
		portStr = v[i+1:]
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("端口非法: %s", portStr)
	}
	host = strings.Trim(host, "[]")
	if strings.TrimSpace(host) == "" {
		return "", 0, fmt.Errorf("host 为空: %s", v)
	}
	return host, port, nil
}
