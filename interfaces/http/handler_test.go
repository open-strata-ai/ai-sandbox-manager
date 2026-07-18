package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/open-strata-ai/ai-sandbox-manager/application/sandbox"
	"github.com/open-strata-ai/ai-sandbox-manager/domain"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/adapter"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/auditmem"
	"github.com/open-strata-ai/ai-sandbox-manager/infrastructure/config"
)

func newServer(t *testing.T, capacity int) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		Enabled:  true,
		Capacity: capacity,
		Pool:     config.PoolConfig{MaxIdlePerSpec: 2, TTLSeconds: 300},
		Defaults: config.DefaultsConfig{Runtime: "kata"},
	}
	adapters := map[string]domain.Sandbox{
		"kata": adapter.NewKataAdapter(os.TempDir()),
		"e2b":  adapter.NewE2BAdapter(os.TempDir()),
	}
	selector := &domain.DefaultSelector{DefaultRuntime: "kata"}
	mgr := sandbox.NewManager(cfg, selector, adapters, auditmem.New())
	t.Cleanup(mgr.Stop)
	return httptest.NewServer(New(mgr).Routes())
}

func TestHTTPEndToEnd(t *testing.T) {
	srv := newServer(t, 64)
	defer srv.Close()
	c := srv.Client()

	// Acquire
	acqBody := `{"runtime":"kata","image":"python"}`
	resp, err := c.Post(srv.URL+"/v1/sandbox/acquire", "application/json", strings.NewReader(acqBody))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var acq struct {
		Handle domain.SandboxHandle `json:"handle"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&acq); err != nil {
		t.Fatalf("decode acquire: %v", err)
	}
	resp.Body.Close()
	if acq.Handle.ID == "" {
		t.Fatal("empty handle id")
	}
	id := acq.Handle.ID

	// Exec
	execBody := `{"code":"print(2*21)","language":"python"}`
	resp, err = c.Post(srv.URL+"/v1/sandbox/"+id+"/exec", "application/json", strings.NewReader(execBody))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	var res domain.ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode exec: %v", err)
	}
	resp.Body.Close()
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "42") {
		t.Fatalf("unexpected exec result: %+v", res)
	}

	// Release
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/sandbox/"+id+"/release", nil)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("release status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Pool stats
	resp, err = c.Get(srv.URL + "/v1/sandbox/pool/stats")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stats status = %d", resp.StatusCode)
	}

	// Health
	resp, err = c.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	var hs domain.HealthStatus
	json.NewDecoder(resp.Body).Decode(&hs)
	resp.Body.Close()
	if !hs.Healthy || !hs.Enabled {
		t.Fatalf("unexpected health: %+v", hs)
	}
}

func TestHTTPExecUnknownSandbox(t *testing.T) {
	srv := newServer(t, 64)
	defer srv.Close()
	body := `{"code":"x","language":"python"}`
	resp, err := srv.Client().Post(srv.URL+"/v1/sandbox/ghost/exec", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHTTPCapacityExhausted(t *testing.T) {
	srv := newServer(t, 0) // capacity 0 -> immediate 429
	defer srv.Close()
	body := `{"runtime":"kata","image":"python"}`
	resp, err := srv.Client().Post(srv.URL+"/v1/sandbox/acquire", "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
}
