package domain

import "testing"

func TestDefaultSelector(t *testing.T) {
	s := &DefaultSelector{DefaultRuntime: "kata", TenantPrefs: map[string]string{"t-e2b": "e2b"}}

	// GPU > 0 forces kata (E2B has no GPU passthrough).
	if got := s.Select(SandboxSpec{GPU: 1}, "any"); got != "kata" {
		t.Fatalf("GPU>0: want kata, got %s", got)
	}
	// Tenant preference wins when GPU==0.
	if got := s.Select(SandboxSpec{}, "t-e2b"); got != "e2b" {
		t.Fatalf("tenant pref: want e2b, got %s", got)
	}
	// Default fallback.
	if got := s.Select(SandboxSpec{}, "t-kata"); got != "kata" {
		t.Fatalf("default: want kata, got %s", got)
	}
	// Default can be flipped to e2b.
	s2 := &DefaultSelector{DefaultRuntime: "e2b"}
	if got := s2.Select(SandboxSpec{}, "x"); got != "e2b" {
		t.Fatalf("default e2b: want e2b, got %s", got)
	}
}

func TestPoolKeyStable(t *testing.T) {
	a := SandboxSpec{Runtime: "kata", CPU: "1", Memory: "512Mi", Image: "python"}
	b := a
	if PoolKey(a) != PoolKey(b) {
		t.Fatal("same spec must yield same key")
	}
	c := a
	c.Image = "node"
	if PoolKey(a) == PoolKey(c) {
		t.Fatal("different image must yield different key")
	}
	// GPU is part of the bucket key so GPU requests never share a warm sandbox.
	d := a
	d.GPU = 1
	if PoolKey(a) == PoolKey(d) {
		t.Fatal("GPU must differentiate the bucket key")
	}
}

func TestSpecHash(t *testing.T) {
	h := SpecHash(SandboxSpec{Runtime: "kata", CPU: "1", Memory: "512Mi", Image: "python"})
	if h == "" {
		t.Fatal("empty hash")
	}
	if h != SpecHash(SandboxSpec{Runtime: "kata", CPU: "1", Memory: "512Mi", Image: "python"}) {
		t.Fatal("hash must be stable")
	}
}
