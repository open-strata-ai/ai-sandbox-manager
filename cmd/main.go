// Command ai-sandbox-manager is the entrypoint. It bootstraps the offline
// wiring (local subprocess adapters + in-memory pool/audit) so the service runs
// and is testable without Kubernetes/ArgoCD/Redis/PostgreSQL.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/open-strata-ai/ai-sandbox-manager/application/sandbox"
	"github.com/open-strata-ai/ai-sandbox-manager/domain"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/adapter"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/auditmem"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/config"
	httphandler "github.com/open-strata-ai/ai-sandbox-manager/interfaces/http"
)

func main() {
	cfg, err := config.Load(os.Getenv("CONFIG_PATH"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	mgr := Bootstrap(cfg)
	defer mgr.Stop()

	h := httphandler.New(mgr)
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: h.Routes()}
	log.Printf("ai-sandbox-manager listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

// Bootstrap wires the default offline composition. Production swaps in a
// PostgreSQL AuditStore, a Redis-backed pool, and real Kata/E2B bindings.
func Bootstrap(cfg config.Config) *sandbox.Manager {
	base := os.TempDir()
	adapters := map[string]domain.Sandbox{
		"kata": adapter.NewKataAdapter(base),
		"e2b":  adapter.NewE2BAdapter(base),
	}
	selector := &domain.DefaultSelector{
		DefaultRuntime: cfg.Defaults.Runtime,
		TenantPrefs:    map[string]string{},
	}
	return sandbox.NewManager(cfg, selector, adapters, auditmem.New())
}
