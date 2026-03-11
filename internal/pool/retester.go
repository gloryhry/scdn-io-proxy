package pool

import (
	"context"
	"log"
	"sync"
	"time"

	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/store"
)

type Retester struct {
	St  *store.Store
	Cfg *config.Manager
	Log *log.Logger
}

type RetestResult struct {
	Total        int      `json:"total"`
	Tested       int      `json:"tested"`
	Updated      int      `json:"updated"`
	Removed      int      `json:"removed"`
	Errors       int      `json:"errors"`
	SampleErrors []string `json:"sample_errors,omitempty"`
	DurationMS   int64    `json:"duration_ms"`
	TestURL      string   `json:"test_url"`
}

func (r *Retester) Run(ctx context.Context) {
	if r.Log == nil {
		r.Log = log.Default()
	}

	for {
		s := r.Cfg.Get()
		interval := time.Duration(s.RetestIntervalSeconds) * time.Second
		if interval < time.Second {
			interval = time.Second
		}

		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			_, _ = r.RetestOnce(ctx)
		}
	}
}

func (r *Retester) RetestOnce(ctx context.Context) (RetestResult, error) {
	start := time.Now()
	var result RetestResult

	if r.St == nil || r.Cfg == nil {
		return result, nil
	}
	if r.Log == nil {
		r.Log = log.Default()
	}

	s := r.Cfg.Get()
	result.TestURL = s.TestURL
	timeout := time.Duration(s.TestTimeoutSeconds) * time.Second
	if timeout < time.Second {
		timeout = 5 * time.Second
	}

	limit := s.TestConcurrency
	if limit < 1 {
		limit = 1
	}

	proxies, err := r.St.ListProxies(ctx)
	if err != nil {
		r.Log.Printf("[retest] list proxies 失败: %v", err)
		return result, err
	}
	result.Total = len(proxies)
	if len(proxies) == 0 {
		return result, nil
	}

	r.Log.Printf("[retest] start total=%d test_url=%s timeout=%ds", len(proxies), s.TestURL, s.TestTimeoutSeconds)

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

	for _, p := range proxies {
		select {
		case <-ctx.Done():
			break
		default:
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(p store.Proxy) {
			defer wg.Done()
			defer func() { <-sem }()

			mu.Lock()
			result.Tested++
			mu.Unlock()

			u := FromStoreProxy(p)
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			latency, err := TestUpstream(cctx, u, s.TestURL, timeout)
			if err != nil {
				if derr := r.St.DeleteProxyByID(ctx, p.ID); derr != nil {
					mu.Lock()
					result.Errors++
					mu.Unlock()
					addSample(derr)
					return
				}
				mu.Lock()
				result.Removed++
				mu.Unlock()
				addSample(err)
				return
			}
			if err := r.St.UpsertUsableProxy(ctx, store.Proxy{
				Protocol:    p.Protocol,
				CountryCode: p.CountryCode,
				IP:          p.IP,
				Port:        p.Port,
			}, time.Now(), latency); err != nil {
				mu.Lock()
				result.Errors++
				mu.Unlock()
				addSample(err)
				return
			}
			mu.Lock()
			result.Updated++
			mu.Unlock()
		}(p)
	}

	wg.Wait()

	result.DurationMS = time.Since(start).Milliseconds()
	r.Log.Printf("[retest] done total=%d tested=%d updated=%d removed=%d errors=%d cost=%dms",
		result.Total, result.Tested, result.Updated, result.Removed, result.Errors, result.DurationMS)
	return result, nil
}
