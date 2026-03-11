package proxy

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"scdn-io-proxy/internal/store"
)

type ExitSnapshot struct {
	HasProxy   bool
	Proxy      store.Proxy
	SwitchedAt time.Time
	ExpiresAt  time.Time
}

type Rotator struct {
	st *store.Store

	mu  sync.Mutex
	idx int

	snap atomic.Value // ExitSnapshot
}

func NewRotator(st *store.Store) *Rotator {
	r := &Rotator{st: st}
	r.snap.Store(ExitSnapshot{})
	return r
}

func (r *Rotator) Snapshot() ExitSnapshot {
	v := r.snap.Load()
	if v == nil {
		return ExitSnapshot{}
	}
	return v.(ExitSnapshot)
}

func (r *Rotator) Advance(ctx context.Context, hold time.Duration) error {
	if hold <= 0 {
		hold = time.Minute
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	list, err := r.st.ListProxies(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	if len(list) == 0 {
		r.idx = 0
		r.snap.Store(ExitSnapshot{
			HasProxy:   false,
			SwitchedAt: now,
			ExpiresAt:  now.Add(hold),
		})
		return nil
	}

	if r.idx >= len(list) {
		r.idx = 0
	}
	p := list[r.idx]
	r.idx++
	if r.idx >= len(list) {
		r.idx = 0
	}

	r.snap.Store(ExitSnapshot{
		HasProxy:   true,
		Proxy:      p,
		SwitchedAt: now,
		ExpiresAt:  now.Add(hold),
	})
	return nil
}
