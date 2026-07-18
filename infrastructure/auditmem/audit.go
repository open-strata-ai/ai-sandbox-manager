// Package auditmem provides an in-memory AuditStore used as the offline default
// (production swaps in a PostgreSQL-backed implementation of domain.AuditStore).
package auditmem

import (
	"context"
	"sync"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

// Store is a thread-safe in-memory AuditStore.
type Store struct {
	mu   sync.Mutex
	rows []domain.AuditRecord
}

func New() *Store { return &Store{} }

func (s *Store) Record(_ context.Context, r domain.AuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows = append(s.rows, r)
	return nil
}

// Rows returns a copy of the recorded audit rows (for tests/observability).
func (s *Store) Rows() []domain.AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.AuditRecord, len(s.rows))
	copy(out, s.rows)
	return out
}
