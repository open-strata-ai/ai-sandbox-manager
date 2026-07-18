// Package sandbox is the application layer: it orchestrates the domain
// (provider selection, pool keying) with the infrastructure adapters (Sandbox
// SPI, idle pool, audit) to expose the sandbox lifecycle use cases
// (acquire / execute / release / stats).
package sandbox

import (
	"context"
	"sync"
	"time"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/config"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/pool"
)

// Manager implements the sandbox lifecycle (R1/R5/R6/R7) with pooled reuse
// (RULE-SB-001), provider routing (RULE-SB-005) and backpressure (RULE-SB-008).
type Manager struct {
	cfg      config.Config
	selector domain.ProviderSelector
	adapters map[string]domain.Sandbox
	audit    domain.AuditStore
	pool     *pool.BucketPool
	enabled  bool

	mu     sync.Mutex
	leases map[string]leaseEntry
}

type leaseEntry struct {
	key    string
	handle domain.SandboxHandle
}

// NewManager wires the Manager. adapters maps provider name -> Sandbox impl;
// a non-nil audit store is required (may be auditmem in offline mode).
func NewManager(cfg config.Config, selector domain.ProviderSelector, adapters map[string]domain.Sandbox, audit domain.AuditStore) *Manager {
	m := &Manager{
		cfg:      cfg,
		selector: selector,
		adapters: adapters,
		audit:    audit,
		enabled:  cfg.Enabled,
		leases:   make(map[string]leaseEntry),
	}
	destroy := func(h domain.SandboxHandle) error {
		if a, ok := m.adapters[h.Runtime]; ok {
			return a.Release(context.Background(), h)
		}
		return nil
	}
	m.pool = pool.New(time.Duration(cfg.Pool.TTLSeconds)*time.Second, cfg.Pool.MaxIdlePerSpec, destroy)
	m.pool.StartReaper(30 * time.Second)
	return m
}

// Acquire gets a sandbox: warm pool reuse first, else cold create (subject to
// node capacity / backpressure).
func (m *Manager) Acquire(ctx context.Context, tenantID string, spec domain.SandboxSpec) (domain.SandboxHandle, error) {
	if !m.enabled {
		return domain.SandboxHandle{}, domain.NewError(domain.ErrProviderDisabled, 503, "sandbox manager disabled")
	}
	provider := m.selector.Select(spec, tenantID)
	adapter, ok := m.adapters[provider]
	if !ok {
		return domain.SandboxHandle{}, domain.NewError(domain.ErrProviderDisabled, 503, "provider not available: "+provider)
	}
	key := domain.PoolKey(spec)
	if h, ok := m.pool.TryAcquire(key); ok {
		m.recordLease(h.ID, key, *h)
		return *h, nil
	}
	if m.pool.Total() >= m.cfg.Capacity {
		return domain.SandboxHandle{}, domain.NewError(domain.ErrPoolExhausted, 429, "sandbox pool exhausted")
	}
	h, err := adapter.Acquire(ctx, spec)
	if err != nil {
		return domain.SandboxHandle{}, err
	}
	m.pool.RegisterCold(key, h)
	m.recordLease(h.ID, key, h)
	return h, nil
}

// Execute runs code in a leased sandbox and audits the outcome (RULE-SB-015).
func (m *Manager) Execute(ctx context.Context, tenantID string, h domain.SandboxHandle, req domain.ExecRequest) (domain.ExecResult, error) {
	adapter, ok := m.adapters[h.Runtime]
	if !ok {
		return domain.ExecResult{}, domain.NewError(domain.ErrProviderDisabled, 503, "provider not available: "+h.Runtime)
	}
	res, err := adapter.Execute(ctx, h, req)
	if m.audit != nil {
		_ = m.audit.Record(ctx, domain.AuditRecord{
			TenantID:      tenantID,
			Runtime:       h.Runtime,
			ExitCode:      res.ExitCode,
			DurationMs:    res.DurationMs,
			ResourceUsage: res.ResourceUsage,
			CreatedAt:     time.Now(),
		})
	}
	return res, err
}

// Release returns (warm) or destroys (pool full) a leased sandbox.
func (m *Manager) Release(ctx context.Context, h domain.SandboxHandle) error {
	m.mu.Lock()
	e, ok := m.leases[h.ID]
	if ok {
		delete(m.leases, h.ID)
	}
	m.mu.Unlock()
	if !ok {
		return domain.NewError(domain.ErrSandboxNotFound, 404, "unknown sandbox: "+h.ID)
	}
	if accepted := m.pool.Return(e.key, h); !accepted {
		if a, ok := m.adapters[h.Runtime]; ok {
			return a.Release(ctx, h) // pool full -> destroy (RULE-SB-002)
		}
	}
	return nil
}

// Handle resolves a leased handle by id (needed by the stateless HTTP layer).
func (m *Manager) Handle(id string) (domain.SandboxHandle, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.leases[id]
	return e.handle, ok
}

// Stats returns aggregate pool statistics.
func (m *Manager) Stats(_ context.Context) domain.PoolStats {
	buckets := m.pool.Stats()
	totalIdle, totalActive := 0, 0
	for _, b := range buckets {
		totalIdle += b.Idle
		totalActive += b.Active
	}
	return domain.PoolStats{Pools: buckets, TotalIdle: totalIdle, TotalActive: totalActive}
}

// Enabled reports whether the sandbox component is on (§11.7).
func (m *Manager) Enabled() bool { return m.enabled }

// Adapters exposes provider readiness for /healthz.
func (m *Manager) Adapters() map[string]domain.Sandbox { return m.adapters }

// Stop terminates the reaper goroutine.
func (m *Manager) Stop() { m.pool.Stop() }

func (m *Manager) recordLease(id, key string, h domain.SandboxHandle) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leases[id] = leaseEntry{key: key, handle: h}
}
