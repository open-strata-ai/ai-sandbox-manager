// Package http is the access layer (①): a net/http router exposing the
// sandbox-manager API (SPECS §7.1). Go 1.22 method+path routing is used.
package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/open-strata-ai/ai-sandbox-manager/application/sandbox"
	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

// Handler wires the sandbox Manager to HTTP.
type Handler struct {
	mgr *sandbox.Manager
}

func New(mgr *sandbox.Manager) *Handler { return &Handler{mgr: mgr} }

// Routes returns the configured mux.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sandbox/acquire", h.acquire)
	mux.HandleFunc("POST /v1/sandbox/{id}/exec", h.exec)
	mux.HandleFunc("POST /v1/sandbox/{id}/release", h.release)
	mux.HandleFunc("GET /v1/sandbox/pool/stats", h.stats)
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /metrics", h.metrics)
	return mux
}

func (h *Handler) acquire(w http.ResponseWriter, r *http.Request) {
	var spec domain.SandboxSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeErr(w, domain.NewError(domain.ErrInvalidSpec, 400, "bad body: "+err.Error()))
		return
	}
	if spec.Runtime == "" {
		spec.Runtime = "kata"
	}
	hnd, err := h.mgr.Acquire(r.Context(), r.Header.Get("X-Tenant-Id"), spec)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"handle": hnd})
}

func (h *Handler) exec(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hnd, ok := h.mgr.Handle(id)
	if !ok {
		writeErr(w, domain.NewError(domain.ErrSandboxNotFound, 404, "unknown sandbox: "+id))
		return
	}
	var req domain.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, domain.NewError(domain.ErrInvalidSpec, 400, "bad body: "+err.Error()))
		return
	}
	res, err := h.mgr.Execute(r.Context(), r.Header.Get("X-Tenant-Id"), hnd, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (h *Handler) release(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hnd, ok := h.mgr.Handle(id)
	if !ok {
		writeErr(w, domain.NewError(domain.ErrSandboxNotFound, 404, "unknown sandbox: "+id))
		return
	}
	if err := h.mgr.Release(r.Context(), hnd); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, h.mgr.Stats(r.Context()))
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	st := domain.HealthStatus{Healthy: true, Enabled: h.mgr.Enabled(), Providers: map[string]bool{}}
	for name, a := range h.mgr.Adapters() {
		hs := a.Health(r.Context())
		st.Providers[name] = hs.Healthy
		if !hs.Healthy {
			st.Healthy = false
		}
	}
	if !st.Enabled {
		st.Healthy = false
		st.Details = "sandbox manager disabled"
	}
	writeJSON(w, 200, st)
}

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	st := h.mgr.Stats(r.Context())
	w.Header().Set("content-type", "text/plain; version=0.0.4")
	for k, b := range st.Pools {
		fmt.Fprintf(w, "sandbox_pool_idle{bucket=%q} %d\n", k, b.Idle)
		fmt.Fprintf(w, "sandbox_pool_active{bucket=%q} %d\n", k, b.Active)
	}
	fmt.Fprintf(w, "sandbox_total_idle %d\n", st.TotalIdle)
	fmt.Fprintf(w, "sandbox_total_active %d\n", st.TotalActive)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var se *domain.SandboxError
	if e, ok := err.(*domain.SandboxError); ok {
		se = e
	} else {
		se = domain.NewError(domain.ErrInternal, 500, err.Error())
	}
	writeJSON(w, se.HTTPStatus, map[string]any{"error": se.Code, "message": se.Msg})
}
