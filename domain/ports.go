package domain

import (
	"context"
	"time"
)

// Sandbox is the unified SPI (interface_versions.Sandbox = 1.0.0, DESIGN §3.2).
// Kata Containers and E2B both implement it (§10.4 multiple implementations).
type Sandbox interface {
	// Acquire gets/creates a sandbox (warm from pool or cold) for the spec.
	Acquire(ctx context.Context, spec SandboxSpec) (SandboxHandle, error)
	// Execute runs code in the given sandbox and returns stdout/stderr/exit code.
	Execute(ctx context.Context, h SandboxHandle, req ExecRequest) (ExecResult, error)
	// Release returns/destroys the sandbox.
	Release(ctx context.Context, h SandboxHandle) error
	// Health reports provider readiness.
	Health(ctx context.Context) HealthStatus
}

// ProviderSelector routes a spec+tenant to a provider name (DESIGN §3.3 / RULE-SB-005).
type ProviderSelector interface {
	Select(spec SandboxSpec, tenantID string) string // "kata" | "e2b"
}

// SandboxPool is the idle-pool abstraction (DESIGN §3.4), infra-implemented.
type SandboxPool interface {
	// TryAcquire takes a warm idle sandbox from the bucket, incrementing active.
	TryAcquire(specKey string) (*SandboxHandle, bool)
	// RegisterCold records a freshly created (leased) sandbox.
	RegisterCold(specKey string, h SandboxHandle)
	// Return puts a leased sandbox back; returns false when the bucket is full
	// (caller must destroy). Always decrements active.
	Return(specKey string, h SandboxHandle) (accepted bool)
	// ReapExpired destroys idle sandboxes past their TTL; returns count destroyed.
	ReapExpired(now time.Time) int
	// Stats returns per-bucket stats.
	Stats() map[string]PoolBucketStats
	// Total is active+idle across all buckets (used for backpressure, RULE-SB-008).
	Total() int
	// Stop terminates the background reaper.
	Stop()
}

// AuditStore persists execution audit rows (SPECS §8.2, RULE-SB-015).
type AuditStore interface {
	Record(ctx context.Context, r AuditRecord) error
}
