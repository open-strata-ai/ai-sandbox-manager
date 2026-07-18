// Package pool implements the idle sandbox pool (DESIGN §3.4 / §5.1 / RULE-SB-006):
// one bucket per SandboxSpec, warm reuse, capacity-bounded idle, and TTL reaping.
package pool

import (
	"sync"
	"time"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

type idleEntry struct {
	h         domain.SandboxHandle
	expiresAt time.Time
}

type bucket struct {
	key     string
	idle    []idleEntry
	active  int
	maxIdle int
}

// BucketPool is a concurrency-safe, in-memory SandboxPool. In production the
// pool metadata lives in Redis (SPECS §8.3); this implementation is the
// offline-verifiable stand-in.
type BucketPool struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	ttl     time.Duration
	maxIdle int
	destroy func(domain.SandboxHandle) error
	clock   func() time.Time
	stop    chan struct{}
	once    sync.Once
}

// New builds a BucketPool. destroy is invoked on TTL-expired idle sandboxes.
func New(ttl time.Duration, maxIdle int, destroy func(domain.SandboxHandle) error) *BucketPool {
	return &BucketPool{
		buckets: map[string]*bucket{},
		ttl:     ttl,
		maxIdle: maxIdle,
		destroy: destroy,
		clock:   time.Now,
		stop:    make(chan struct{}),
	}
}

func (p *BucketPool) get(key string) *bucket {
	b, ok := p.buckets[key]
	if !ok {
		b = &bucket{key: key, maxIdle: p.maxIdle}
		p.buckets[key] = b
	}
	return b
}

func (p *BucketPool) TryAcquire(key string) (*domain.SandboxHandle, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.get(key)
	if len(b.idle) == 0 {
		return nil, false
	}
	e := b.idle[len(b.idle)-1]
	b.idle = b.idle[:len(b.idle)-1]
	b.active++
	return &e.h, true
}

func (p *BucketPool) RegisterCold(key string, h domain.SandboxHandle) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.get(key).active++
}

func (p *BucketPool) Return(key string, h domain.SandboxHandle) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.get(key)
	b.active--
	if len(b.idle) < b.maxIdle {
		b.idle = append(b.idle, idleEntry{h: h, expiresAt: p.clock().Add(p.ttl)})
		return true
	}
	return false
}

func (p *BucketPool) ReapExpired(now time.Time) int {
	p.mu.Lock()
	var expired []domain.SandboxHandle
	for _, b := range p.buckets {
		kept := b.idle[:0]
		for _, e := range b.idle {
			if e.expiresAt.Before(now) {
				expired = append(expired, e.h)
			} else {
				kept = append(kept, e)
			}
		}
		b.idle = kept
	}
	p.mu.Unlock()

	for _, h := range expired {
		if p.destroy != nil {
			_ = p.destroy(h) // best effort
		}
	}
	return len(expired)
}

func (p *BucketPool) Stats() map[string]domain.PoolBucketStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]domain.PoolBucketStats, len(p.buckets))
	for k, b := range p.buckets {
		out[k] = domain.PoolBucketStats{Idle: len(b.idle), Active: b.active, MaxIdle: b.maxIdle}
	}
	return out
}

func (p *BucketPool) Total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := 0
	for _, b := range p.buckets {
		t += b.active + len(b.idle)
	}
	return t
}

// StartReaper launches the background TTL scanner (DESIGN §5.1 / RULE-SB-001).
func (p *BucketPool) StartReaper(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case now := <-t.C:
				p.ReapExpired(now)
			}
		}
	}()
}

func (p *BucketPool) Stop() {
	p.once.Do(func() { close(p.stop) })
}
