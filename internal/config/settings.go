package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type Settings struct {
	SCDNAPIKey string `json:"scdn_api_key"`

	FetchProtocol        string `json:"fetch_protocol"` // http | socks5
	CountryCode          string `json:"country_code"`   // ISO 3166-1 alpha2 或 all
	FetchCount           int    `json:"fetch_count"`
	FetchIntervalSeconds int    `json:"fetch_interval_seconds"`

	TestURL               string `json:"test_url"`
	TestTimeoutSeconds    int    `json:"test_timeout_seconds"`
	RetestIntervalSeconds int    `json:"retest_interval_seconds"`
	TestConcurrency       int    `json:"test_concurrency"`

	ExitHoldSeconds int `json:"exit_hold_seconds"`

	ProxyAuthUser string `json:"proxy_auth_user"`
	ProxyAuthPass string `json:"proxy_auth_pass"`
	AdminAuthUser string `json:"admin_auth_user"`
	AdminAuthPass string `json:"admin_auth_pass"`
}

func DefaultSettings() Settings {
	return Settings{
		FetchProtocol:        "http",
		CountryCode:          "all",
		FetchCount:           20,
		FetchIntervalSeconds: 60,

		TestURL:               "https://www.example.com/",
		TestTimeoutSeconds:    10,
		RetestIntervalSeconds: 300,
		TestConcurrency:       10,

		ExitHoldSeconds: 60,

		ProxyAuthUser: "proxy",
		ProxyAuthPass: "proxy",
		// 默认管理后台与代理服务共用同一套账号（AdminCreds 内会回退到 ProxyAuth）。
		AdminAuthUser: "",
		AdminAuthPass: "",
	}
}

var countryCodeRe = regexp.MustCompile(`(?i)^(all|[a-z]{2})$`)

func (s Settings) Normalized() Settings {
	s.FetchProtocol = strings.ToLower(strings.TrimSpace(s.FetchProtocol))
	s.CountryCode = strings.ToUpper(strings.TrimSpace(s.CountryCode))
	if strings.EqualFold(s.CountryCode, "ALL") {
		s.CountryCode = "all"
	}
	return s
}

func (s Settings) Validate() error {
	s = s.Normalized()

	switch s.FetchProtocol {
	case "http", "https", "socks5":
	default:
		return fmt.Errorf("fetch_protocol 必须是 http、https 或 socks5")
	}

	if !countryCodeRe.MatchString(s.CountryCode) {
		return fmt.Errorf("country_code 必须是 ISO 3166-1 alpha2（如 US）或 all")
	}

	if s.FetchCount < 1 || s.FetchCount > 20 {
		return fmt.Errorf("fetch_count 取值范围 1-20")
	}
	if s.FetchIntervalSeconds < 1 {
		return fmt.Errorf("fetch_interval_seconds 必须 >= 1")
	}

	u, err := url.Parse(s.TestURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("test_url 非法")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("test_url 仅支持 http 或 https")
	}
	if s.TestTimeoutSeconds < 1 {
		return fmt.Errorf("test_timeout_seconds 必须 >= 1")
	}
	if s.RetestIntervalSeconds < 1 {
		return fmt.Errorf("retest_interval_seconds 必须 >= 1")
	}
	if s.TestConcurrency < 1 || s.TestConcurrency > 200 {
		return fmt.Errorf("test_concurrency 取值范围 1-200")
	}

	if s.ExitHoldSeconds < 1 {
		return fmt.Errorf("exit_hold_seconds 必须 >= 1")
	}

	if strings.TrimSpace(s.ProxyAuthUser) == "" || strings.TrimSpace(s.ProxyAuthPass) == "" {
		return fmt.Errorf("proxy_auth_user/proxy_auth_pass 不能为空")
	}

	// 管理后台账号可留空，表示跟随代理认证账号。
	return nil
}

type SettingsPatch struct {
	SCDNAPIKey *string `json:"scdn_api_key,omitempty"`

	FetchProtocol        *string `json:"fetch_protocol,omitempty"`
	CountryCode          *string `json:"country_code,omitempty"`
	FetchCount           *int    `json:"fetch_count,omitempty"`
	FetchIntervalSeconds *int    `json:"fetch_interval_seconds,omitempty"`

	TestURL               *string `json:"test_url,omitempty"`
	TestTimeoutSeconds    *int    `json:"test_timeout_seconds,omitempty"`
	RetestIntervalSeconds *int    `json:"retest_interval_seconds,omitempty"`
	TestConcurrency       *int    `json:"test_concurrency,omitempty"`

	ExitHoldSeconds *int `json:"exit_hold_seconds,omitempty"`

	ProxyAuthUser *string `json:"proxy_auth_user,omitempty"`
	ProxyAuthPass *string `json:"proxy_auth_pass,omitempty"`
	AdminAuthUser *string `json:"admin_auth_user,omitempty"`
	AdminAuthPass *string `json:"admin_auth_pass,omitempty"`
}

func (p SettingsPatch) IsEmpty() bool {
	return p.SCDNAPIKey == nil &&
		p.FetchProtocol == nil &&
		p.CountryCode == nil &&
		p.FetchCount == nil &&
		p.FetchIntervalSeconds == nil &&
		p.TestURL == nil &&
		p.TestTimeoutSeconds == nil &&
		p.RetestIntervalSeconds == nil &&
		p.TestConcurrency == nil &&
		p.ExitHoldSeconds == nil &&
		p.ProxyAuthUser == nil &&
		p.ProxyAuthPass == nil &&
		p.AdminAuthUser == nil &&
		p.AdminAuthPass == nil
}

func (s Settings) ApplyPatch(p SettingsPatch) Settings {
	if p.SCDNAPIKey != nil {
		// 空字符串视为不修改，避免UI里不小心清空密钥。
		if strings.TrimSpace(*p.SCDNAPIKey) != "" {
			s.SCDNAPIKey = *p.SCDNAPIKey
		}
	}

	if p.FetchProtocol != nil {
		s.FetchProtocol = *p.FetchProtocol
	}
	if p.CountryCode != nil {
		s.CountryCode = *p.CountryCode
	}
	if p.FetchCount != nil {
		s.FetchCount = *p.FetchCount
	}
	if p.FetchIntervalSeconds != nil {
		s.FetchIntervalSeconds = *p.FetchIntervalSeconds
	}

	if p.TestURL != nil {
		s.TestURL = *p.TestURL
	}
	if p.TestTimeoutSeconds != nil {
		s.TestTimeoutSeconds = *p.TestTimeoutSeconds
	}
	if p.RetestIntervalSeconds != nil {
		s.RetestIntervalSeconds = *p.RetestIntervalSeconds
	}
	if p.TestConcurrency != nil {
		s.TestConcurrency = *p.TestConcurrency
	}

	if p.ExitHoldSeconds != nil {
		s.ExitHoldSeconds = *p.ExitHoldSeconds
	}

	if p.ProxyAuthUser != nil {
		if strings.TrimSpace(*p.ProxyAuthUser) != "" {
			s.ProxyAuthUser = *p.ProxyAuthUser
		}
	}
	if p.ProxyAuthPass != nil {
		if strings.TrimSpace(*p.ProxyAuthPass) != "" {
			s.ProxyAuthPass = *p.ProxyAuthPass
		}
	}
	if p.AdminAuthUser != nil {
		// 允许置空：表示跟随 ProxyAuthUser
		s.AdminAuthUser = strings.TrimSpace(*p.AdminAuthUser)
	}
	if p.AdminAuthPass != nil {
		// 空字符串视为不修改；置空可通过 AdminAuthUser 为空实现（跟随 proxy）
		if strings.TrimSpace(*p.AdminAuthPass) != "" {
			s.AdminAuthPass = *p.AdminAuthPass
		}
	}

	return s
}
