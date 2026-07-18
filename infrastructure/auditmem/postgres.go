package auditmem

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/lib/pq"
	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

// Postgres is a PostgreSQL-backed AuditStore.
type Postgres struct {
	db *sql.DB
}

// NewPostgres opens a connection and ensures the audit_records table exists.
func NewPostgres(dsn string) (*Postgres, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("audit postgres: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("audit postgres: ping: %w", err)
	}
	if _, err := db.Exec(migrateSandboxAudit); err != nil {
		return nil, fmt.Errorf("audit postgres: migrate: %w", err)
	}
	return &Postgres{db: db}, nil
}

const migrateSandboxAudit = `
CREATE TABLE IF NOT EXISTS sandbox_audit_records (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      TEXT NOT NULL DEFAULT '',
    runtime        TEXT NOT NULL DEFAULT '',
    exit_code      INT NOT NULL DEFAULT 0,
    duration_ms    INT NOT NULL DEFAULT 0,
    resource_usage JSONB NOT NULL DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

func (p *Postgres) Record(_ context.Context, r domain.AuditRecord) error {
	ruJSON, _ := json.Marshal(r.ResourceUsage)
	_, err := p.db.Exec(
		`INSERT INTO sandbox_audit_records (tenant_id,runtime,exit_code,duration_ms,resource_usage) VALUES ($1,$2,$3,$4,$5::jsonb)`,
		r.TenantID, r.Runtime, r.ExitCode, r.DurationMs, string(ruJSON),
	)
	return err
}
