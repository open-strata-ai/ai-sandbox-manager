package domain

import (
	"fmt"
	"strings"
)

// DefaultSelector implements ProviderSelector per RULE-SB-005:
//  1. spec.GPU > 0  -> force "kata" (E2B has no GPU passthrough)
//  2. tenant preference (if any, and GPU == 0) -> that provider
//  3. otherwise the configured default runtime (currently "kata")
type DefaultSelector struct {
	DefaultRuntime string
	TenantPrefs    map[string]string // tenantID -> "kata"|"e2b"
}

func (s *DefaultSelector) Select(spec SandboxSpec, tenantID string) string {
	if spec.GPU > 0 {
		return "kata"
	}
	if pref, ok := s.TenantPrefs[tenantID]; ok && (pref == "kata" || pref == "e2b") {
		return pref
	}
	if s.DefaultRuntime == "e2b" {
		return "e2b"
	}
	return "kata"
}

// PoolKey is the human-readable bucket label used in pool stats (SPECS §7.3).
// Bucketed by the resource-defining dimensions (DESIGN §6.5: runtime/CPU/mem/image),
// with GPU added so GPU vs non-GPU requests never share a warm sandbox.
func PoolKey(spec SandboxSpec) string {
	return fmt.Sprintf("%s:%s:%s:%d:%s",
		spec.Runtime, spec.CPU, spec.Memory, spec.GPU, spec.Image)
}

// SpecHash returns a stable hash of the spec for storage keys
// (SPECS §8.3 `sandbox:pool:{spec_hash}`).
func SpecHash(spec SandboxSpec) string {
	return strings.ToLower(fmt.Sprintf("%x",
		[]byte(PoolKey(spec))))
}
