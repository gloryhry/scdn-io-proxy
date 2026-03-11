package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"scdn-io-proxy/internal/store"
)

type Manager struct {
	st *store.Store

	mu sync.Mutex
	v  atomic.Value // Settings
}

func NewManager(ctx context.Context, st *store.Store, initial Settings) (*Manager, error) {
	if st == nil {
		return nil, fmt.Errorf("store 不能为空")
	}
	initial = initial.Normalized()
	if err := initial.Validate(); err != nil {
		return nil, err
	}

	m := &Manager{st: st}
	jsonStr, ok, err := st.GetSettingsJSON(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		var fromDB Settings
		if err := json.Unmarshal([]byte(jsonStr), &fromDB); err != nil {
			return nil, fmt.Errorf("解析 settings 失败: %w", err)
		}
		fromDB = fromDB.Normalized()
		if err := fromDB.Validate(); err != nil {
			return nil, fmt.Errorf("数据库 settings 非法: %w", err)
		}
		m.v.Store(fromDB)
		return m, nil
	}

	b, err := json.Marshal(initial)
	if err != nil {
		return nil, err
	}
	if err := st.UpsertSettingsJSON(ctx, string(b)); err != nil {
		return nil, err
	}
	m.v.Store(initial)
	return m, nil
}

func (m *Manager) Get() Settings {
	if m == nil {
		return DefaultSettings()
	}
	v := m.v.Load()
	if v == nil {
		return DefaultSettings()
	}
	return v.(Settings)
}

func (m *Manager) Update(ctx context.Context, patch SettingsPatch) (Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.Get()
	next := cur.ApplyPatch(patch).Normalized()
	if err := next.Validate(); err != nil {
		return cur, err
	}

	b, err := json.Marshal(next)
	if err != nil {
		return cur, err
	}
	if err := m.st.UpsertSettingsJSON(ctx, string(b)); err != nil {
		return cur, err
	}
	m.v.Store(next)
	return next, nil
}

func (m *Manager) AdminCreds() (string, string) {
	s := m.Get()
	u := s.AdminAuthUser
	p := s.AdminAuthPass
	if u == "" {
		u = s.ProxyAuthUser
		p = s.ProxyAuthPass
	}
	return u, p
}
