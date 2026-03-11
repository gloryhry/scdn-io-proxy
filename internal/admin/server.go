package admin

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/pool"
	"scdn-io-proxy/internal/proxy"
	"scdn-io-proxy/internal/store"
)

type Server struct {
	Addr    string
	St      *store.Store
	Cfg     *config.Manager
	Rotator *proxy.Rotator

	Fetcher  *pool.Fetcher
	Retester *pool.Retester

	Log *log.Logger
}

func (s *Server) Serve(ctx context.Context) error {
	if s.Log == nil {
		s.Log = log.Default()
	}

	webFS, err := fs.Sub(assets, "web")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	fileServer := http.FileServer(http.FS(webFS))
	// net/http 会将 /index.html 重定向到 ./，若我们手动把 / 重写到 /index.html 会造成重定向死循环。
	// 这里让 FileServer 直接处理 /，它会自动返回 index.html。
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings.html", http.StatusFound)
	})
	mux.Handle("/", fileServer)

	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/proxies", s.handleProxies)
	mux.HandleFunc("/api/proxies/delete", s.handleProxiesDelete)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/actions/fetch", s.handleActionFetch)
	mux.HandleFunc("/api/actions/retest", s.handleActionRetest)
	mux.HandleFunc("/api/actions/rotate", s.handleActionRotate)

	handler := s.basicAuth(mux)

	srv := &http.Server{
		Addr:              s.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.Log.Printf("[admin] listening on %s", s.Addr)
	if _, port, err := net.SplitHostPort(strings.TrimSpace(s.Addr)); err == nil && port == "6000" {
		s.Log.Printf("[admin] 注意：Chromium 内核浏览器通常会拦截 6000 端口（ERR_UNSAFE_PORT），请改用 8080/6001 等端口访问管理后台")
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancel()
	}()

	err = srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p := s.Cfg.AdminCreds()

		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(auth, "Basic ") {
			unauthorized(w)
			return
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[len("Basic "):]))
		if err != nil {
			unauthorized(w)
			return
		}
		got := string(raw)
		expect := u + ":" + p
		if len(got) != len(expect) || subtle.ConstantTimeCompare([]byte(got), []byte(expect)) != 1 {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="scdn-io-proxy-admin"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("unauthorized\n"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap := s.Rotator.Snapshot()
	poolSize, _ := s.St.CountProxies(ctx)

	type exit struct {
		Protocol    string `json:"protocol"`
		CountryCode string `json:"country_code"`
		Addr        string `json:"addr"`
	}
	var cur *exit
	remain := int64(0)
	expMs := int64(0)
	if snap.HasProxy {
		cur = &exit{
			Protocol:    snap.Proxy.Protocol,
			CountryCode: snap.Proxy.CountryCode,
			Addr:        snap.Proxy.Addr(),
		}
		remain = int64(time.Until(snap.ExpiresAt).Seconds())
		if remain < 0 {
			remain = 0
		}
		expMs = snap.ExpiresAt.UnixMilli()
	}

	resp := map[string]any{
		"pool_size":              poolSize,
		"current_exit":           cur,
		"exit_remaining_seconds": remain,
		"exit_expires_at_ms":     expMs,
	}
	writeJSON(w, resp)
}

func (s *Server) handleProxies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("page_size"), 50)
	switch pageSize {
	case 10, 50, 100:
	default:
		pageSize = 50
	}

	total, err := s.St.CountProxies(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if total == 0 {
		writeJSON(w, map[string]any{
			"items":       []any{},
			"total":       0,
			"page":        1,
			"page_size":   pageSize,
			"total_pages": 0,
		})
		return
	}

	totalPages := (total + pageSize - 1) / pageSize
	if page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * pageSize

	list, err := s.St.ListProxiesPage(ctx, pageSize, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, map[string]any{
			"id":                   p.ID,
			"protocol":             p.Protocol,
			"country_code":         p.CountryCode,
			"addr":                 p.Addr(),
			"last_test_at_ms":      p.LastTestAt.UnixMilli(),
			"last_test_latency_ms": p.LastTestLatencyMS,
		})
	}
	writeJSON(w, map[string]any{
		"items":       out,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

func (s *Server) handleProxiesDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	type reqBody struct {
		IDs []int64 `json:"ids"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req reqBody
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.IDs) == 0 {
		http.Error(w, "ids 不能为空", http.StatusBadRequest)
		return
	}
	if len(req.IDs) > 5000 {
		http.Error(w, "ids 数量过大", http.StatusBadRequest)
		return
	}

	deleted, err := s.St.DeleteProxiesByIDs(r.Context(), req.IDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 如果当前出口被删除，尽快切换，避免 UI 继续展示不存在的出口。
	snap := s.Rotator.Snapshot()
	if snap.HasProxy {
		needRotate := false
		for _, id := range req.IDs {
			if id == snap.Proxy.ID {
				needRotate = true
				break
			}
		}
		if needRotate {
			hold := time.Duration(s.Cfg.Get().ExitHoldSeconds) * time.Second
			_ = s.Rotator.Advance(r.Context(), hold)
		}
	}

	writeJSON(w, map[string]any{"deleted": deleted})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		set := s.Cfg.Get()
		writeJSON(w, maskSettings(set))
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var patch config.SettingsPatch
		if err := json.Unmarshal(body, &patch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		next, err := s.Cfg.Update(r.Context(), patch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, maskSettings(next))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func maskSettings(s config.Settings) map[string]any {
	return map[string]any{
		"has_scdn_api_key":        s.SCDNAPIKey != "",
		"fetch_protocol":          s.FetchProtocol,
		"country_code":            s.CountryCode,
		"fetch_count":             s.FetchCount,
		"fetch_interval_seconds":  s.FetchIntervalSeconds,
		"test_url":                s.TestURL,
		"test_timeout_seconds":    s.TestTimeoutSeconds,
		"retest_interval_seconds": s.RetestIntervalSeconds,
		"test_concurrency":        s.TestConcurrency,
		"exit_hold_seconds":       s.ExitHoldSeconds,
		"proxy_auth_user":         s.ProxyAuthUser,
		"admin_auth_user":         s.AdminAuthUser,
		"has_proxy_auth_pass":     s.ProxyAuthPass != "",
		"has_admin_auth_pass":     s.AdminAuthPass != "",
	}
}

func (s *Server) handleActionFetch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	res, err := s.Fetcher.FetchOnce(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, res)
}

func (s *Server) handleActionRetest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	res, err := s.Retester.RetestOnce(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, res)
}

func (s *Server) handleActionRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	hold := time.Duration(s.Cfg.Get().ExitHoldSeconds) * time.Second
	if err := s.Rotator.Advance(r.Context(), hold); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	snap := s.Rotator.Snapshot()
	writeJSON(w, map[string]any{
		"has_proxy":   snap.HasProxy,
		"proxy":       snap.Proxy.Addr(),
		"switched_at": snap.SwitchedAt.UnixMilli(),
		"expires_at":  snap.ExpiresAt.UnixMilli(),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func parsePositiveInt(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return def
	}
	return n
}
