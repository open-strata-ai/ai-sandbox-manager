package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

// FakeSandbox is an in-memory domain.Sandbox used to assert the SPI contract
// (DESIGN §10.4: multiple implementations satisfy Sandbox) without subprocesses.
type FakeSandbox struct {
	runtime string
}

func (f *FakeSandbox) Acquire(_ context.Context, spec domain.SandboxSpec) (domain.SandboxHandle, error) {
	return domain.SandboxHandle{ID: "fake-1", Runtime: f.runtime, Network: spec.Network}, nil
}
func (f *FakeSandbox) Execute(_ context.Context, _ domain.SandboxHandle, req domain.ExecRequest) (domain.ExecResult, error) {
	return domain.ExecResult{Stdout: "ok:" + req.Language, ExitCode: 0}, nil
}
func (f *FakeSandbox) Release(_ context.Context, _ domain.SandboxHandle) error { return nil }
func (f *FakeSandbox) Health(_ context.Context) domain.HealthStatus {
	return domain.HealthStatus{Healthy: true, Providers: map[string]bool{f.runtime: true}}
}

// runContract exercises the acquire -> exec -> release -> health path that any
// Sandbox implementation must satisfy.
func runContract(t *testing.T, sb domain.Sandbox) {
	t.Helper()
	ctx := context.Background()
	h, err := sb.Acquire(ctx, domain.SandboxSpec{Runtime: "kata", Network: domain.NetworkDenyAll})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if h.ID == "" {
		t.Fatal("Acquire returned empty handle")
	}
	res, err := sb.Execute(ctx, h, domain.ExecRequest{Code: "x", Language: "python"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("Execute exit code = %d", res.ExitCode)
	}
	if err := sb.Release(ctx, h); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !sb.Health(ctx).Healthy {
		t.Fatal("Health reported unhealthy")
	}
}

func TestFakeSandboxContract(t *testing.T) {
	runContract(t, &FakeSandbox{runtime: "kata"})
	runContract(t, &FakeSandbox{runtime: "e2b"})
}

func TestLocalProcessAdapterExecutes(t *testing.T) {
	sb := NewLocalAdapter("kata", os.TempDir())
	ctx := context.Background()

	h, err := sb.Acquire(ctx, domain.SandboxSpec{Runtime: "kata", Network: domain.NetworkDenyAll})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if h.Network != domain.NetworkDenyAll {
		t.Fatalf("isolation policy not recorded: %v", h.Network)
	}

	res, err := sb.Execute(ctx, h, domain.ExecRequest{Code: "print(6*7)", Language: "python"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "42") {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := sb.Release(ctx, h); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Temp dir must be cleaned up (RULE-SB-014).
	if _, statErr := os.Stat(filepath.Join(os.TempDir(), h.ID)); !os.IsNotExist(statErr) {
		t.Fatalf("sandbox dir not cleaned: %v", statErr)
	}
}

func TestLocalProcessAdapterUnsupportedLanguage(t *testing.T) {
	sb := NewLocalAdapter("kata", os.TempDir())
	ctx := context.Background()
	h, _ := sb.Acquire(ctx, domain.SandboxSpec{Runtime: "kata"})
	_, err := sb.Execute(ctx, h, domain.ExecRequest{Code: "x", Language: "cobol"})
	if err == nil {
		t.Fatal("expected error for unsupported language")
	}
	if e, ok := err.(*domain.SandboxError); !ok || e.Code != domain.ErrInvalidSpec {
		t.Fatalf("expected ErrInvalidSpec, got %v", err)
	}
}
