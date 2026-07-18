// Package adapter holds the SPI implementations of domain.Sandbox.
//
// In production, ai-sandbox-manager backs the Sandbox SPI with Kata Containers
// (K8s RuntimeClass) and E2B (Firecracker microVM) — two implementations behind
// one interface (DESIGN §10.4). To keep the module offline-verifiable, both
// adapters delegate to LocalProcessAdapter, a real OS-subprocess executor that
// honors the full lifecycle (acquire -> exec -> release) and records the
// requested isolation policy on the handle (RULE-SB-003/004).
package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/open-strata-ai/ai-sandbox-manager/domain"
)

// LocalProcessAdapter executes code in OS subprocesses. It is the offline
// stand-in for Kata/E2B and the engine that actually runs in `go test`.
type LocalProcessAdapter struct {
	runtime string
	baseDir string
}

// NewKataAdapter builds the Kata adapter (production: K8s RuntimeClass=kata).
func NewKataAdapter(baseDir string) *LocalProcessAdapter {
	return &LocalProcessAdapter{runtime: "kata", baseDir: baseDir}
}

// NewE2BAdapter builds the E2B adapter (production: Firecracker microVM).
func NewE2BAdapter(baseDir string) *LocalProcessAdapter {
	return &LocalProcessAdapter{runtime: "e2b", baseDir: baseDir}
}

// NewLocalAdapter builds a generic local adapter (used in tests).
func NewLocalAdapter(runtime, baseDir string) *LocalProcessAdapter {
	return &LocalProcessAdapter{runtime: runtime, baseDir: baseDir}
}

func (a *LocalProcessAdapter) Acquire(_ context.Context, spec domain.SandboxSpec) (domain.SandboxHandle, error) {
	dir, err := os.MkdirTemp(a.baseDir, "sb-")
	if err != nil {
		return domain.SandboxHandle{}, domain.NewError(domain.ErrInternal, 500, "create sandbox dir: "+err.Error())
	}
	return domain.SandboxHandle{
		ID:       filepath.Base(dir),
		Runtime:  a.runtime,
		Endpoint: "local://" + filepath.Base(dir),
		LeasedAt: time.Now().Unix(),
		Network:  spec.Network,
		TTL:      spec.TTL,
	}, nil
}

func (a *LocalProcessAdapter) Execute(ctx context.Context, h domain.SandboxHandle, req domain.ExecRequest) (domain.ExecResult, error) {
	_ = h // isolation policy is recorded on the handle; real enforcement is by
	// K8s NetworkPolicy / E2B firewall in production (RULE-SB-011).

	start := time.Now()
	var cmd *exec.Cmd
	switch req.Language {
	case "python", "py":
		cmd = exec.CommandContext(ctx, "python3", "-c", req.Code)
	case "node", "js":
		cmd = exec.CommandContext(ctx, "node", "-e", req.Code)
	case "shell", "sh", "bash":
		cmd = exec.CommandContext(ctx, "sh", "-c", req.Code)
	default:
		return domain.ExecResult{}, domain.NewError(domain.ErrInvalidSpec, 400, "unsupported language: "+req.Language)
	}
	if len(req.Env) > 0 {
		cmd.Env = append(cmd.Env, os.Environ()...)
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	dur := int(time.Since(start).Milliseconds())
	res := domain.ExecResult{
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: dur,
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			res.ExitCode = -1 // RULE-SB-013: timeout
			return res, nil
		}
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		res.ExitCode = -1
		res.Stderr = res.Stderr + err.Error()
		return res, nil
	}
	res.ExitCode = 0
	return res, nil
}

func (a *LocalProcessAdapter) Release(_ context.Context, h domain.SandboxHandle) error {
	dir := filepath.Join(a.baseDir, h.ID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release sandbox %s: %w", h.ID, err)
	}
	return nil
}

func (a *LocalProcessAdapter) Health(_ context.Context) domain.HealthStatus {
	return domain.HealthStatus{
		Healthy:   true,
		Enabled:   true,
		Providers: map[string]bool{a.runtime: true},
		Details:   a.runtime + " local executor ready",
	}
}
