// Package domain holds the pure, dependency-free core of ai-sandbox-manager:
// the Sandbox SPI value types (DESIGN §3.2), the provider-selection / pool
// keying logic (§3.3/§6.5), and the domain errors. No external frameworks.
package domain

import "time"

// NetworkPolicy is the sandbox network isolation policy (DESIGN §3.2 / §5.2).
type NetworkPolicy string

const (
	NetworkDenyAll     NetworkPolicy = "deny-all"
	NetworkAllowSameNS NetworkPolicy = "allow-same-ns"
	NetworkEgressAllow NetworkPolicy = "egress-allowlist"
)

// SandboxSpec describes the requested sandbox shape (DESIGN §3.2).
type SandboxSpec struct {
	Runtime   string        // kata | e2b (selected by ProviderSelector, §10.4)
	CPU       string        // e.g. "1"
	Memory    string        // e.g. "512Mi"
	GPU       int           // 0 = none; Kata supports passthrough (§4.3.3)
	Network   NetworkPolicy // deny-all | allow-same-ns | egress-allowlist
	TimeoutMs int
	Image     string // runtime image (python|node|shell)
	TTL       int    // idle reclaim seconds
}

// ExecRequest is a code-execution request (DESIGN §3.2).
type ExecRequest struct {
	Code     string   // source code
	Language string   // python|node|shell
	Deps     []string // pip/npm dependencies
	Args     []string
	Env      map[string]string
}

// ResourceUsage is the resource consumption of a single execution.
type ResourceUsage struct {
	CPUms    int
	MemBytes int
	GPUms    int
}

// ExecResult is the outcome of a code execution.
type ExecResult struct {
	Stdout        string
	Stderr        string
	ExitCode      int // -1 = timeout/killed (RULE-SB-013)
	DurationMs    int
	ResourceUsage ResourceUsage
}

// SandboxHandle identifies a leased sandbox.
type SandboxHandle struct {
	ID       string
	Runtime  string
	Endpoint string // execution proxy address inside the sandbox
	LeasedAt int64  // Unix timestamp
	// The fields below are recorded at Acquire time for audit/isolation tracing
	// (DESIGN §5.2) without leaking user code.
	Network NetworkPolicy
	TTL     int
}

// HealthStatus reports sandbox-manager readiness (SPECS §11.3).
type HealthStatus struct {
	Healthy   bool
	Enabled   bool
	Providers map[string]bool
	Details   string
}

// PoolBucketStats is per-bucket pool statistics (DESIGN §3.4).
type PoolBucketStats struct {
	Idle    int
	Active  int
	MaxIdle int
}

// PoolStats aggregates per-bucket stats for /v1/sandbox/pool/stats.
type PoolStats struct {
	Pools       map[string]PoolBucketStats
	TotalIdle   int
	TotalActive int
}

// AuditRecord is the append-only audit row (SPECS §8.2). No code body is stored.
type AuditRecord struct {
	TenantID      string
	Runtime       string
	ExitCode      int
	DurationMs    int
	ResourceUsage ResourceUsage
	CreatedAt     time.Time
}
