package admin

import (
	"context"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"scdn-io-proxy/internal/config"
	"scdn-io-proxy/internal/store"
)

func TestAdminStaticRoot_NoRedirectLoop(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "admin-test.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	set := config.DefaultSettings()
	set.ProxyAuthUser = "u"
	set.ProxyAuthPass = "p"
	set.AdminAuthUser = "admin"
	set.AdminAuthPass = "pass"
	if err := set.Validate(); err != nil {
		t.Fatalf("settings validate: %v", err)
	}
	cfgMgr, err := config.NewManager(ctx, st, set)
	if err != nil {
		t.Fatalf("config.NewManager: %v", err)
	}

	srv := &Server{
		St:  st,
		Cfg: cfgMgr,
	}

	// 复用 Serve 的静态路由配置逻辑（手工搭一个 handler 即可）。
	webFS, err := fs.Sub(assets, "web")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	mux := http.NewServeMux()
	fileServer := http.FileServer(http.FS(webFS))
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings.html", http.StatusFound)
	})
	mux.Handle("/", fileServer)
	handler := srv.basicAuth(mux)

	req := httptest.NewRequest(http.MethodGet, "http://example.local/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:pass")))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "SCDN 代理池中转") {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}
