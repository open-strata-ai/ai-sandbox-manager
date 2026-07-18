package pool

import (
	"testing"
	"time"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

func TestBucketPoolLifecycle(t *testing.T) {
	var destroyed int
	p := New(time.Minute, 2, func(h domain.SandboxHandle) error { destroyed++; return nil })
	defer p.Stop()

	key := "kata:1:512Mi:0:python"
	if _, ok := p.TryAcquire(key); ok {
		t.Fatal("empty pool must not yield a handle")
	}

	h1 := domain.SandboxHandle{ID: "h1", Runtime: "kata"}
	// A cold-created (leased) handle is active but NOT yet in the idle pool.
	p.RegisterCold(key, h1)
	if _, ok := p.TryAcquire(key); ok {
		t.Fatal("leased handle must not be returned by TryAcquire before Return")
	}

	// Return accepts (idle < maxIdle) and makes it reusable (warm).
	if !p.Return(key, h1) {
		t.Fatal("Return should accept when below maxIdle")
	}
	got, ok := p.TryAcquire(key)
	if !ok || got.ID != "h1" {
		t.Fatalf("expected warm h1, got %+v ok=%v", got, ok)
	}
	// Return it again so stats reflect idle=1.
	if !p.Return(key, *got) {
		t.Fatal("second Return should accept")
	}

	st := p.Stats()[key]
	if st.Idle != 1 || st.Active != 0 || st.MaxIdle != 2 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if p.Total() != 1 {
		t.Fatalf("Total should be 1, got %d", p.Total())
	}
	if destroyed != 0 {
		t.Fatalf("nothing should be destroyed, got %d", destroyed)
	}
}

func TestBucketPoolFull(t *testing.T) {
	p := New(time.Minute, 1, nil)
	defer p.Stop()
	key := "kata:1:512Mi:0:python"

	h1 := domain.SandboxHandle{ID: "h1", Runtime: "kata"}
	p.RegisterCold(key, h1)
	if !p.Return(key, h1) {
		t.Fatal("first Return should be accepted")
	}
	// Second Return exceeds maxIdle=1 -> rejected (caller must destroy).
	h2 := domain.SandboxHandle{ID: "h2", Runtime: "kata"}
	p.RegisterCold(key, h2)
	if p.Return(key, h2) {
		t.Fatal("second Return should be rejected (pool full)")
	}
}

func TestBucketPoolReap(t *testing.T) {
	var destroyed []string
	p := New(time.Minute, 4, func(h domain.SandboxHandle) error {
		destroyed = append(destroyed, h.ID)
		return nil
	})
	defer p.Stop()

	// Control the clock so we can force TTL expiry deterministically.
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.clock = func() time.Time { return fixed }

	key := "kata:1:512Mi:0:python"
	h1 := domain.SandboxHandle{ID: "h1", Runtime: "kata"}
	p.RegisterCold(key, h1)
	p.Return(key, h1) // idle, expiresAt = fixed + ttl

	// Not yet expired.
	if n := p.ReapExpired(fixed); n != 0 {
		t.Fatalf("should not reap before TTL, got %d", n)
	}
	// Past TTL -> reaped + destroyed.
	if n := p.ReapExpired(fixed.Add(2 * time.Minute)); n != 1 {
		t.Fatalf("should reap 1, got %d", n)
	}
	if len(destroyed) != 1 || destroyed[0] != "h1" {
		t.Fatalf("destroy callback wrong: %v", destroyed)
	}
}
