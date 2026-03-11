package config

import (
	"flag"
	"fmt"
)

type AppConfig struct {
	ListenAddr string
	AdminAddr  string
	DBPath     string
}

func ParseAppConfig() (AppConfig, Settings, SettingsPatch) {
	var cfg AppConfig
	var s Settings

	flag.StringVar(&cfg.ListenAddr, "listen", "0.0.0.0:1080", "代理服务监听地址（HTTP/SOCKS5同端口）")
	flag.StringVar(&cfg.AdminAddr, "admin", "127.0.0.1:8080", "管理后台监听地址")
	flag.StringVar(&cfg.DBPath, "db", "proxy-pool.sqlite", "SQLite数据库文件路径")

	flag.StringVar(&s.SCDNAPIKey, "scdn-api-key", "", "SCDN API Key（首次启动可用，之后以数据库设置为准）")
	flag.StringVar(&s.FetchProtocol, "fetch-protocol", DefaultSettings().FetchProtocol, "拉取代理协议：http/https/socks5")
	flag.StringVar(&s.CountryCode, "country", DefaultSettings().CountryCode, "国家/地区（ISO 3166-1 alpha2，例如 US；或 all）")
	flag.IntVar(&s.FetchCount, "fetch-count", DefaultSettings().FetchCount, "每次拉取代理数量（1-20）")
	flag.IntVar(&s.FetchIntervalSeconds, "fetch-interval", DefaultSettings().FetchIntervalSeconds, "拉取间隔（秒）")
	flag.StringVar(&s.TestURL, "test-url", DefaultSettings().TestURL, "代理可用性测试URL（http/https）")
	flag.IntVar(&s.TestTimeoutSeconds, "test-timeout", DefaultSettings().TestTimeoutSeconds, "单次测试超时（秒）")
	flag.IntVar(&s.RetestIntervalSeconds, "retest-interval", DefaultSettings().RetestIntervalSeconds, "池内代理复测间隔（秒）")
	flag.IntVar(&s.ExitHoldSeconds, "exit-hold", DefaultSettings().ExitHoldSeconds, "出口代理保持时长（秒，到期后新连接切换）")
	flag.IntVar(&s.TestConcurrency, "test-concurrency", DefaultSettings().TestConcurrency, "并发测试数量")
	flag.StringVar(&s.ProxyAuthUser, "proxy-user", DefaultSettings().ProxyAuthUser, "下游代理认证用户名（HTTP/SOCKS5）")
	flag.StringVar(&s.ProxyAuthPass, "proxy-pass", DefaultSettings().ProxyAuthPass, "下游代理认证密码（HTTP/SOCKS5）")
	flag.StringVar(&s.AdminAuthUser, "admin-user", DefaultSettings().AdminAuthUser, "管理后台认证用户名（为空则使用 proxy-user）")
	flag.StringVar(&s.AdminAuthPass, "admin-pass", DefaultSettings().AdminAuthPass, "管理后台认证密码（为空则使用 proxy-pass）")

	flag.Parse()

	visited := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { visited[f.Name] = true })

	var patch SettingsPatch
	if visited["scdn-api-key"] {
		patch.SCDNAPIKey = ptrString(s.SCDNAPIKey)
	}
	if visited["fetch-protocol"] {
		patch.FetchProtocol = ptrString(s.FetchProtocol)
	}
	if visited["country"] {
		patch.CountryCode = ptrString(s.CountryCode)
	}
	if visited["fetch-count"] {
		patch.FetchCount = ptrInt(s.FetchCount)
	}
	if visited["fetch-interval"] {
		patch.FetchIntervalSeconds = ptrInt(s.FetchIntervalSeconds)
	}
	if visited["test-url"] {
		patch.TestURL = ptrString(s.TestURL)
	}
	if visited["test-timeout"] {
		patch.TestTimeoutSeconds = ptrInt(s.TestTimeoutSeconds)
	}
	if visited["retest-interval"] {
		patch.RetestIntervalSeconds = ptrInt(s.RetestIntervalSeconds)
	}
	if visited["test-concurrency"] {
		patch.TestConcurrency = ptrInt(s.TestConcurrency)
	}
	if visited["exit-hold"] {
		patch.ExitHoldSeconds = ptrInt(s.ExitHoldSeconds)
	}
	if visited["proxy-user"] {
		patch.ProxyAuthUser = ptrString(s.ProxyAuthUser)
	}
	if visited["proxy-pass"] {
		patch.ProxyAuthPass = ptrString(s.ProxyAuthPass)
	}
	if visited["admin-user"] {
		patch.AdminAuthUser = ptrString(s.AdminAuthUser)
	}
	if visited["admin-pass"] {
		patch.AdminAuthPass = ptrString(s.AdminAuthPass)
	}

	return cfg, s, patch
}

func (c AppConfig) Validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen 不能为空")
	}
	if c.AdminAddr == "" {
		return fmt.Errorf("admin 不能为空")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db 不能为空")
	}
	return nil
}

func ptrString(v string) *string { return &v }
func ptrInt(v int) *int          { return &v }
