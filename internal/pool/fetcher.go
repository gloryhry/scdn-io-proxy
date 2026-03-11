package pool

import (
	"context"
	"log"
	"net/url"
	"sync"
	"time"

	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/scdn"
	"scdn-io-proxy/internal/store"
)

type Fetcher struct {
	St  *store.Store
	Cfg *config.Manager
	Log *log.Logger
}

type FetchResult struct {
	Returned        int      `json:"returned"`
	Candidates      int      `json:"candidates"`
	Tested          int      `json:"tested"`
	Inserted        int      `json:"inserted"`
	Failed          int      `json:"failed"`
	StoreErrors     int      `json:"store_errors"`
	SampleErrors    []string `json:"sample_errors,omitempty"`
	DurationMS      int64    `json:"duration_ms"`
	FetchProtocol   string   `json:"fetch_protocol"`
	CountryCode     string   `json:"country_code"`
	TestURL         string   `json:"test_url"`
	TestTimeoutSecs int      `json:"test_timeout_seconds"`
}

func (f *Fetcher) Run(ctx context.Context) {
	if f.Log == nil {
		f.Log = log.Default()
	}

	// 启动时先来一轮，尽快把池子填起来。
	_, _ = f.FetchOnce(ctx)

	for {
		s := f.Cfg.Get()
		interval := time.Duration(s.FetchIntervalSeconds) * time.Second
		if interval < time.Second {
			interval = time.Second
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			_, _ = f.FetchOnce(ctx)
		}
	}
}

func (f *Fetcher) FetchOnce(ctx context.Context) (FetchResult, error) {
	start := time.Now()
	var result FetchResult

	if f.St == nil || f.Cfg == nil {
		return result, nil
	}
	if f.Log == nil {
		f.Log = log.Default()
	}

	s := f.Cfg.Get()
	result.FetchProtocol = s.FetchProtocol
	result.CountryCode = s.CountryCode
	result.TestURL = s.TestURL
	result.TestTimeoutSecs = s.TestTimeoutSeconds

	f.Log.Printf("[fetch] start protocol=%s country=%s count=%d test_url=%s timeout=%ds",
		s.FetchProtocol, s.CountryCode, s.FetchCount, s.TestURL, s.TestTimeoutSeconds)

	c := scdn.NewClient(s.SCDNAPIKey)
	resp, err := c.GetProxy(ctx, scdn.GetProxyRequest{
		Protocol:    s.FetchProtocol,
		CountryCode: s.CountryCode,
		Count:       s.FetchCount,
	})
	if err != nil {
		f.Log.Printf("[fetch] 拉取失败: %v", err)
		return result, err
	}
	result.Returned = len(resp.Proxies)

	seen := make(map[string]struct{}, len(resp.Proxies))
	var candidates []Upstream
	for _, raw := range resp.Proxies {
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}

		ip, port, err := ParseIPPort(raw)
		if err != nil {
			continue
		}
		candidates = append(candidates, Upstream{
			Protocol:    s.FetchProtocol,
			CountryCode: s.CountryCode,
			IP:          ip,
			Port:        port,
		})
	}

	if len(candidates) == 0 {
		result.DurationMS = time.Since(start).Milliseconds()
		f.Log.Printf("[fetch] done returned=%d candidates=0 cost=%dms", result.Returned, result.DurationMS)
		return result, nil
	}
	result.Candidates = len(candidates)

	timeout := time.Duration(s.TestTimeoutSeconds) * time.Second
	if timeout < time.Second {
		timeout = 5 * time.Second
	}

	limit := s.TestConcurrency
	if limit < 1 {
		limit = 1
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)

	var mu sync.Mutex
	addSample := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if len(result.SampleErrors) >= 5 {
			return
		}
		result.SampleErrors = append(result.SampleErrors, err.Error())
	}

	for _, u := range candidates {
		select {
		case <-ctx.Done():
			break
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(u Upstream) {
			defer wg.Done()
			defer func() { <-sem }()

			mu.Lock()
			result.Tested++
			mu.Unlock()

			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			latency, err := TestUpstream(cctx, u, s.TestURL, timeout)
			if err != nil {
				mu.Lock()
				result.Failed++
				mu.Unlock()
				addSample(err)
				return
			}

			if err := f.St.UpsertUsableProxy(ctx, store.Proxy{
				Protocol:    u.Protocol,
				CountryCode: u.CountryCode,
				IP:          u.IP,
				Port:        u.Port,
			}, time.Now(), latency); err != nil {
				mu.Lock()
				result.StoreErrors++
				mu.Unlock()
				addSample(err)
				return
			}
			mu.Lock()
			result.Inserted++
			mu.Unlock()
		}(u)
	}
	wg.Wait()

	result.DurationMS = time.Since(start).Milliseconds()
	f.Log.Printf("[fetch] done returned=%d candidates=%d tested=%d inserted=%d failed=%d store_err=%d cost=%dms",
		result.Returned, result.Candidates, result.Tested, result.Inserted, result.Failed, result.StoreErrors, result.DurationMS)

	if result.Inserted == 0 {
		if tu, err := url.Parse(s.TestURL); err == nil && tu.Scheme == "https" && s.FetchProtocol == "http" {
			f.Log.Printf("[fetch] 提示：test_url 为 https，fetch_protocol 建议使用 https（更可能返回支持 CONNECT 的代理）")
		}
		if len(result.SampleErrors) > 0 {
			f.Log.Printf("[fetch] 失败样例: %s", result.SampleErrors[0])
		}
	}

	return result, nil
}
