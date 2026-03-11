package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"scdn-io-proxy/internal/admin"
	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/pool"
	"scdn-io-proxy/internal/proxy"
	"scdn-io-proxy/internal/store"
)

func main() {
	appCfg, initialSettings, overrides := config.ParseAppConfig()
	if err := appCfg.Validate(); err != nil {
		log.Printf("配置错误: %v", err)
		os.Exit(2)
	}

	st, err := store.Open(appCfg.DBPath)
	if err != nil {
		log.Printf("打开数据库失败: %v", err)
		os.Exit(2)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgMgr, err := config.NewManager(ctx, st, initialSettings)
	if err != nil {
		log.Printf("加载 settings 失败: %v", err)
		os.Exit(2)
	}
	if !overrides.IsEmpty() {
		if _, err := cfgMgr.Update(ctx, overrides); err != nil {
			log.Printf("启动参数覆盖 settings 失败: %v", err)
			os.Exit(2)
		}
	}

	rot := proxy.NewRotator(st)
	go runRotator(ctx, rot, cfgMgr)

	fetcher := &pool.Fetcher{St: st, Cfg: cfgMgr}
	retester := &pool.Retester{St: st, Cfg: cfgMgr}
	go fetcher.Run(ctx)
	go retester.Run(ctx)

	adminSrv := &admin.Server{
		Addr:     appCfg.AdminAddr,
		St:       st,
		Cfg:      cfgMgr,
		Rotator:  rot,
		Fetcher:  fetcher,
		Retester: retester,
	}
	go func() {
		if err := adminSrv.Serve(ctx); err != nil {
			log.Printf("admin server error: %v", err)
			stop()
		}
	}()

	proxySrv := &proxy.Server{
		ListenAddr: appCfg.ListenAddr,
		Rotator:    rot,
		Cfg:        cfgMgr,
	}
	if err := proxySrv.Serve(ctx); err != nil {
		log.Printf("proxy server error: %v", err)
		os.Exit(1)
	}
}

func runRotator(ctx context.Context, rot *proxy.Rotator, cfg *config.Manager) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// 启动时尽快选一个出口。
	_ = rot.Advance(ctx, holdFromSettings(cfg.Get()))

	var lastNoProxyCheck time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		snap := rot.Snapshot()
		now := time.Now()

		if snap.HasProxy {
			if now.Before(snap.ExpiresAt) {
				continue
			}
			_ = rot.Advance(ctx, holdFromSettings(cfg.Get()))
			continue
		}

		// 无出口时，降低 DB 查询频率，但仍尽快“补上”出口。
		if !lastNoProxyCheck.IsZero() && now.Sub(lastNoProxyCheck) < 5*time.Second {
			continue
		}
		lastNoProxyCheck = now
		_ = rot.Advance(ctx, holdFromSettings(cfg.Get()))
	}
}

func holdFromSettings(s config.Settings) time.Duration {
	hold := time.Duration(s.ExitHoldSeconds) * time.Second
	if hold < time.Second {
		hold = time.Second
	}
	return hold
}
