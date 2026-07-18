package pool

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
	"github.com/redis/go-redis/v9"
)

// RedisPool is a Redis-backed SandboxPool using hashes for bucket state.
// Each bucket is a Redis hash: pool:{key}. Fields: active (int), idle (JSON array).
type RedisPool struct {
	client  *redis.Client
	ttl     time.Duration
	maxIdle int
	destroy func(domain.SandboxHandle) error
	clock   func() time.Time
	stop    chan struct{}
	once    sync.Once
}

// NewRedisPool builds a Redis-backed pool for a pre-existing Redis connection.
func NewRedisPool(client *redis.Client, ttl time.Duration, maxIdle int, destroy func(domain.SandboxHandle) error) *RedisPool {
	return &RedisPool{
		client:  client,
		ttl:     ttl,
		maxIdle: maxIdle,
		destroy: destroy,
		clock:   time.Now,
		stop:    make(chan struct{}),
	}
}

func poolKey(specKey string) string { return "pool:" + specKey }

func (p *RedisPool) TryAcquire(specKey string) (*domain.SandboxHandle, bool) {
	ctx := context.Background()
	key := poolKey(specKey)
	// Pop one idle handle from the list atomically
	idleJSON, err := p.client.LIndex(ctx, key+":idle", -1).Result()
	if err != nil {
		return nil, false
	}
	p.client.LTrim(ctx, key+":idle", 0, -2)
	var h domain.SandboxHandle
	json.Unmarshal([]byte(idleJSON), &h)
	p.client.HIncrBy(ctx, key, "active", 1)
	return &h, true
}

func (p *RedisPool) RegisterCold(specKey string, h domain.SandboxHandle) {
	ctx := context.Background()
	key := poolKey(specKey)
	p.client.HIncrBy(ctx, key, "active", 1)
}

func (p *RedisPool) Return(specKey string, h domain.SandboxHandle) bool {
	ctx := context.Background()
	key := poolKey(specKey)
	// Decrement active
	p.client.HIncrBy(ctx, key, "active", -1)
	// Check idle count
	n, _ := p.client.LLen(ctx, key+":idle").Result()
	if int(n) >= p.maxIdle {
		return false // bucket full, caller must destroy
	}
	data, _ := json.Marshal(h)
	p.client.RPush(ctx, key+":idle", string(data))
	// Set expiry so idle handles expire naturally
	p.client.Expire(ctx, key+":idle", p.ttl*2)
	return true
}

func (p *RedisPool) ReapExpired(now time.Time) int {
	// Redis handles TTL expiry; this is a no-op for the Redis pool since
	// idle keys auto-expire. We scan for expired entries as a safety net.
	ctx := context.Background()
	iter := p.client.Scan(ctx, 0, "pool:*:idle", 100).Iterator()
	count := 0
	for iter.Next(ctx) {
		ttl, _ := p.client.TTL(ctx, iter.Val()).Result()
		if ttl < 0 {
			// Already expired, count it
			count++
		}
	}
	return count
}

func (p *RedisPool) Stats() map[string]domain.PoolBucketStats {
	ctx := context.Background()
	iter := p.client.Scan(ctx, 0, "pool:*", 100).Iterator()
	out := make(map[string]domain.PoolBucketStats)
	for iter.Next(ctx) {
		key := iter.Val()
		specKey := key[len("pool:"):]
		if len(specKey) == 0 || key[len(key)-5:] == ":idle" {
			continue
		}
		active, _ := p.client.HGet(ctx, key, "active").Int()
		idleCount, _ := p.client.LLen(ctx, key+":idle").Result()
		out[specKey] = domain.PoolBucketStats{
			Active:  active,
			Idle:    int(idleCount),
			MaxIdle: p.maxIdle,
		}
	}
	return out
}

func (p *RedisPool) Total() int {
	ctx := context.Background()
	iter := p.client.Scan(ctx, 0, "pool:*", 100).Iterator()
	total := 0
	for iter.Next(ctx) {
		key := iter.Val()
		if len(key) > 5 && key[len(key)-5:] == ":idle" {
			continue
		}
		active, _ := p.client.HGet(ctx, key, "active").Int()
		idleCount, _ := p.client.LLen(ctx, key+":idle").Result()
		total += active + int(idleCount)
	}
	return total
}

func (p *RedisPool) Stop() {
	// no-op for Redis pool (no background goroutine)
}
