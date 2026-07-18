package sandbox

import (
	"context"
	"strconv"
	"testing"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/auditmem"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/config"
)

// FakeSandbox is a deterministic in-memory domain.Sandbox for Manager tests.
type FakeSandbox struct {
	runtime  string
	acquires int
	releases int
	seq      int
}

func (f *FakeSandbox) Acquire(_ context.Context, spec domain.SandboxSpec) (domain.SandboxHandle, error) {
	f.acquires++
	f.seq++
	return domain.SandboxHandle{ID: f.runtime + "-h" + strconv.Itoa(f.seq), Runtime: f.runtime, Network: spec.Network}, nil
}
func (f *FakeSandbox) Execute(_ context.Context, _ domain.SandboxHandle, req domain.ExecRequest) (domain.ExecResult, error) {
	return domain.ExecResult{Stdout: "ok", ExitCode: 0}, nil
}
func (f *FakeSandbox) Release(_ context.Context, _ domain.SandboxHandle) error {
	f.releases++
	return nil
}
func (f *FakeSandbox) Health(_ context.Context) domain.HealthStatus {
	return domain.HealthStatus{Healthy: true, Providers: map[string]bool{f.runtime: true}}
}

func newTestManager(t *testing.T, cfg config.Config) (*Manager, *FakeSandbox) {
	t.Helper()
	fake := &FakeSandbox{runtime: "kata"}
	adapters := map[string]domain.Sandbox{"kata": fake}
	selector := &domain.DefaultSelector{DefaultRuntime: "kata"}
	mgr := NewManager(cfg, selector, adapters, auditmem.New())
	t.Cleanup(mgr.Stop)
	return mgr, fake
}

func baseCfg() config.Config {
	return config.Config{
		Enabled:  true,
		Capacity: 64,
		Pool:     config.PoolConfig{MaxIdlePerSpec: 2, TTLSeconds: 300},
		Defaults: config.DefaultsConfig{Runtime: "kata"},
	}
}

func TestManagerWarmReuse(t *testing.T) {
	mgr, _ := newTestManager(t, baseCfg())
	ctx := context.Background()
	spec := domain.SandboxSpec{Runtime: "kata", Image: "python"}

	h1, err := mgr.Acquire(ctx, "t1", spec)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := mgr.Release(ctx, h1); err != nil {
		t.Fatalf("Release: %v", err)
	}
	h2, err := mgr.Acquire(ctx, "t1", spec)
	if err != nil {
		t.Fatalf("Acquire 2: %v", err)
	}
	if h2.ID != h1.ID {
		t.Fatalf("expected warm reuse (same id), got %s vs %s", h1.ID, h2.ID)
	}
}

func TestManagerCapacityBackpressure(t *testing.T) {
	cfg := baseCfg()
	cfg.Capacity = 1
	mgr, _ := newTestManager(t, cfg)
	ctx := context.Background()
	spec := domain.SandboxSpec{Runtime: "kata", Image: "python"}

	if _, err := mgr.Acquire(ctx, "t1", spec); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	_, err := mgr.Acquire(ctx, "t1", spec) // pool empty, total >= capacity
	if err == nil {
		t.Fatal("expected POOL_EXHAUSTED (429)")
	}
	if e, ok := err.(*domain.SandboxError); !ok || e.Code != domain.ErrPoolExhausted || e.HTTPStatus != 429 {
		t.Fatalf("expected ErrPoolExhausted/429, got %v", err)
	}
}

func TestManagerDestroyWhenPoolFull(t *testing.T) {
	cfg := baseCfg()
	cfg.Pool.MaxIdlePerSpec = 1
	mgr, fake := newTestManager(t, cfg)
	ctx := context.Background()
	spec := domain.SandboxSpec{Runtime: "kata", Image: "python"}

	// Two cold sandboxes (idle pool empty), then release both.
	h1, _ := mgr.Acquire(ctx, "t1", spec)
	h2, _ := mgr.Acquire(ctx, "t1", spec)
	if h1.ID == h2.ID {
		t.Fatal("expected distinct handles")
	}
	if err := mgr.Release(ctx, h1); err != nil { // idle=[h1], accepted
		t.Fatalf("release h1: %v", err)
	}
	if err := mgr.Release(ctx, h2); err != nil { // idle full -> destroy h2
		t.Fatalf("release h2: %v", err)
	}
	// idle pool is at maxIdle, so h2 is destroyed via adapter.Release.
	if fake.releases != 1 {
		t.Fatalf("expected 1 destroy (adapter.Release) on full pool, got %d", fake.releases)
	}
}

func TestManagerExecuteAudits(t *testing.T) {
	cfg := baseCfg()
	mgr, _ := newTestManager(t, cfg)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, "tenant-9", domain.SandboxSpec{Runtime: "kata", Image: "python"})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	res, err := mgr.Execute(ctx, "tenant-9", h, domain.ExecRequest{Code: "x", Language: "python"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code %d", res.ExitCode)
	}
	// Audit recorded (RULE-SB-015).
	store := mgr.audit.(*auditmem.Store)
	rows := store.Rows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	if rows[0].TenantID != "tenant-9" || rows[0].Runtime != "kata" {
		t.Fatalf("unexpected audit row: %+v", rows[0])
	}
}

func TestManagerReleaseUnknown(t *testing.T) {
	mgr, _ := newTestManager(t, baseCfg())
	err := mgr.Release(context.Background(), domain.SandboxHandle{ID: "ghost"})
	if e, ok := err.(*domain.SandboxError); !ok || e.Code != domain.ErrSandboxNotFound {
		t.Fatalf("expected ErrSandboxNotFound, got %v", err)
	}
}

func TestManagerDisabled(t *testing.T) {
	cfg := baseCfg()
	cfg.Enabled = false
	mgr, _ := newTestManager(t, cfg)
	_, err := mgr.Acquire(context.Background(), "t1", domain.SandboxSpec{Runtime: "kata"})
	if e, ok := err.(*domain.SandboxError); !ok || e.Code != domain.ErrProviderDisabled {
		t.Fatalf("expected ErrProviderDisabled, got %v", err)
	}
}
