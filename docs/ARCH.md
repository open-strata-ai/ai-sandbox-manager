# ai-sandbox-manager · Architecture (Architecture Overview)

> **Excerpted from** `docs/DESIGN.md` §1 Positioning and Boundaries · §2 List of Responsibilities · §3 Core Interface · §6 Adapter
> **Language · Framework**: Go · Gin + Cobra + Wire (DDD four layers; sandbox pool/scheduling hot path can be on Hertz/go-zero)
> **Domain**: agent-infra (Agent infrastructure layer · sandbox execution environment)
> **optional**: true (optional · optional, off by default; only required by Agents that allow code execution)
> **Platform version**: v1.0.0

---

## §1 Positioning and Boundary (Scope)

### 1.1 Positioning in one sentence

`ai-sandbox-manager` is OpenStrata's **Sandbox Execution Environment Manager**, hosting architecture §4.3.3 "Sandbox Execution Environment". It provides an isolated, restricted, recyclable code execution environment for the Agent, shields the differences between the underlying Kata Containers/E2B, and exposes a unified `Sandbox` SPI (§10.3) to the upper layer (the code class Tool registered with `ToolRegistry` when the Agent is running).

### 1.2 Core Problems Solved

Turn the dangerous thing of "code running in an isolated environment" into a "declarative, quota-based, timeout-able, poolable, auditable" security capability that is not bound to a certain sandbox implementation (Kata / E2B can coexist and switch).

### 1.3 Optionality and applicable scenarios

- **optional**: Default off (§10.2 "Sandbox execution optional (default off)")
- **Enablement conditions**: Only agents that allow code execution require this repository
- **Phase introduction**: Phases 1 to 3 starter/standard are not enabled (`profiles/optional_disabled` includes `ai-sandbox-manager`, `kata-containers`, `e2b`), and are lit from the advanced level
- **Omitted scenarios**: Agents that do not require code execution (such as pure question and answer, API calls)

### 1.4 Architecture role

| Dimensions | Description |
| --- | --- |
| **Level** | DDD four layers: `domain/` (Sandbox Port + pool model entity) · `application/` (pool scheduling/execution case) · `infrastructure/` (KataAdapter/E2BAdapter/Wire DI) · `interfaces/` (Gin handler) |
| **Management plane framework** | Gin (sandbox API) |
| **Hot Path Framework** | Hertz/go-zero (optional, `Acquire`/`Execute` high frequency) |
| **DI solution** | Wire (compile-time dependency injection) |
| **Sandbox Runtime** | Kata Containers (VM level, main work) / E2B Firecracker (microVM, alternative) |

### 1.5 Division of labor with other Go components

| Component | Relationship Type | Description |
| --- | --- | --- |
| `ai-tool-registry` | Indirect dependency (callee) | The code class Tool is registered with tool-registry; the runtime repository serves as `SandboxExecutor` to host its execution (§10.6 Dependency rule `Tool→SandboxExecutor`). |
| `ai-gateway-core` | Independent data plane | The gateway is responsible for "model calling" and does not execute code; the two data planes are independent. |
| `ai-provisioning-engine` | Deployment orchestration | This repository is one of the deployed optional components; its K8s RuntimeClass / E2B credentials are injected by the assembly engine. |

### 1.6 Boundary constraints

| Constraints | Description |
| --- | --- |
| **Do not register the tool** | The tool is managed by `ai-tool-registry`, this repository only receives `ExecRequest` and executes it |
| **No model calling** | Model calling is handled by `ai-gateway-core` |
| **Non-persistent code** | User code only has temporary tmpfs, clean it up after execution |
| **Do not share sandboxes across tenants** | Each `Acquire` returns an independent sandbox and cannot be reused |
| **No network by default** | `NetworkPolicy: deny-all`, to prevent data leakage |

---

## §2 Responsibilities List

### 2.1 Complete list of responsibilities

| # | Responsibilities | Required/Optional | Trigger conditions | Description |
| --- | --- | --- | --- | --- |
| R1 | **Sandbox Lifecycle Management** | optional | Always run after opening | Create/execute/recycle; pooled reuse (§4.3.3 POOL) |
| R2 | **Isolation Policy** | optional | when sandbox is created | VM/microVM level isolation, network isolation, resource restrictions (§4.3.3) |
| R3 | **Kata Adapter** | optional | `providers.kata.enabled: true` | Kata Containers RuntimeClass (main work, §4.3.3) |
| R4 | **E2B Adapter** | optional | `providers.e2b.enabled: true` | E2B Firecracker microVM (optional, §4.3.3) |
| R5 | **Quota/Timeout** | optional | Each execution request | CPU/Mem/GPU, execution timeout, network outbound policy |
| R6 | **Dependency injection execution** | optional | `Execute` call | code + dependency + timeout execution request (§4.3.3 REQ) |
| R7 | **Measurement/Audit** | optional | Each execution is completed | Number of executions, duration, resource consumption, results (§4.8 Audit) |

### 2.2 Responsibility classification

| Level | Responsibility Number | Description |
| --- | --- | --- |
| **Core Path** | R1, R2, R5, R6 | Minimal closed loop for sandbox execution |
| **Adapter** | R3, R4 | Choose one or both (ProviderSelector routing) |
| **Observability** | R7 | Measurement/Audit/Tracking |

### 2.3 Runtime guarantees

| Coverage | Description |
| --- | --- |
| **Isolation Guarantee** | VM-level isolation (Kata KVM / E2B Firecracker), not container-level |
| **Resource Hard Limit** | K8s container limits (CPU/Mem/GPU) + cgroups mandatory |
| **Timeout forced kill** | Watchdog goroutine monitoring, timeout forced recycling |
| **Automatic cleaning** | tmpfs cleaning, no residual code/data |
| **Pooled reuse** | Warm sandbox reuse reduces cold start delay |

---

## §3 Core interface and abstraction

### 3.1 Design principles

Domain layer definition `Sandbox` Port (bom.yaml `interface_versions.Sandbox = 1.0.0`). This repository itself is the implementer, exposing a unified interface to the upper-layer Agent and adapting Kata/E2B differences to the lower-layer Agent.

### 3.2 Sandbox SPI（v1.0.0）

```go
package domain

// ===== Sandbox SPI（interface_versions.Sandbox = 1.0.0）=====
type Sandbox interface {
    //Acquire Get/create a free sandbox from the pool
    Acquire(ctx context.Context, spec SandboxSpec) (SandboxHandle, error)
    //Execute executes code in the specified sandbox and returns stdout/stderr/exit code
    Execute(ctx context.Context, h SandboxHandle, req ExecRequest) (ExecResult, error)
    //Release returns/destroys the sandbox
    Release(ctx context.Context, h SandboxHandle) error
    //Health exploration
    Health(ctx context.Context) HealthStatus
}

type SandboxSpec struct {
    Runtime   string        //kata | e2b (selected by ProviderSelector, §10.4)
    CPU       string        //Such as "1"
    Memory    string        //Such as "512Mi"
    GPU       int           //0 = None; Kata supports passthrough (§4.3.3)
    Network   NetworkPolicy // deny-all | allow-same-ns | egress-allowlist
    TimeoutMs int
    Image     string        //Runtime image (Python/Node/Shell)
    TTL       int           //Idle recycling time in seconds
}

type ExecRequest struct {
    Code     string            //Source code
    Language string            // python|node|shell
    Deps     []string          //pip/npm dependencies
    Args     []string
    Env      map[string]string
}

type ExecResult struct {
    Stdout        string
    Stderr        string
    ExitCode      int
    DurationMs    int
    ResourceUsage  ResourceUsage
}

type SandboxHandle struct {
    ID       string
    Runtime  string
    Endpoint string //Execution proxy address in the container
    LeasedAt int64  // Unix timestamp
}

type ResourceUsage struct {
    CPUms    int
    MemBytes int
    GPUms    int
}

//NetworkPolicy sandbox network policy type
type NetworkPolicy string
const (
    NetworkDenyAll      NetworkPolicy = "deny-all"
    NetworkAllowSameNS  NetworkPolicy = "allow-same-ns"
    NetworkEgressAllow  NetworkPolicy = "egress-allowlist"
)
```

### 3.3 ProviderSelector

```go
//===== ProviderSelector (domain layer) =====
type ProviderSelector interface {
    Select(spec SandboxSpec, tenantID string) string // "kata" | "e2b"
}
```

### 3.4 Pool model interface

```go
//===== SandboxPool (domain layer, infrastructure layer implementation) =====
type SandboxPool interface {
    //TryAcquire obtains an idle sandbox (millisecond level) from the pool of the specified spec. If there is no idle sandbox, it returns nil.
    TryAcquire(specHash string) (*SandboxHandle, bool)
    //PutBack returns the sandbox to the pool
    PutBack(specHash string, h *SandboxHandle) error
    //Stats returns pool status
    Stats() map[string]PoolBucketStats
}

type PoolBucketStats struct {
    Idle    int
    Active  int
    MaxIdle int
}
```

### 3.5 Delay Budget

| operation | warm delay | cold delay | description |
| --- | --- | --- | --- |
| `Acquire` (Kata) | ~1s | ~5s | Reuse and warm up Pod vs create new |
| `Acquire` (E2B) | ~150ms | ~2s | Reuse microVM vs create new |
| `Release` (return pool) | ≤50ms | — | Only refresh TTL, no Pod operations are involved |
| `Execute` | Depends on code | — | Hard constrained by `SandboxSpec.TimeoutMs` |

---

## §6 Adapter and SPI Ecosystem

### 6.1 SPI port matrix

| SPI port | Version | Repository role | External components (bom.yaml) | Default | Alternative | Adapter |
| --- | --- | --- | --- | --- | --- | --- |
| `Sandbox` | 1.0.0 | Implementer | Kata Containers (optional) · E2B (optional) | Alternative | Alternative | `KataAdapter` / `E2BAdapter` |
| `Cache` | 1.0.0 | Consumer | Redis (core) | ✅ | — | Pool metadata, quota count |
| `Auth` | 1.0.0 | Consumer | Keycloak (core) | ✅ | — | Tenant JWT Identity |
| `Tracing` | 1.0.0 | Consumer | Langfuse/OTel (optional/core) | ✅ | — | Perform link tracing |

### 6.2 Sandbox Adapter comparison

| Dimensions | KataAdapter | E2BAdapter |
| --- | --- | --- |
| **Isolation Level** | VM level (standalone KVM) | microVM (Firecracker) |
| **GPU support** | Supported (NVIDIA device plugin pass-through) | Not supported |
| **warm startup** | ~1s (reused Pod) | ~150ms (reused microVM) |
| **cold startup** | ~5s (new Pod) | ~2s (new microVM) |
| **Resource Limits** | K8s limits + cgroups | E2B SandboxTemplate |
| **Network Policy** | K8s NetworkPolicy | E2B Firewall Rules |
| **Filesystem** | tmpfs (emptyDir) | tmpfs (built-in) |
| **Applicable scenarios** | CPU + GPU computing, data science | Pure code execution, fast scenarios |
| **Deployment Requirements** | K8s RuntimeClass `kata` | E2B Cloud/SDK or self-hosted |
| **Default state** | `enabled: true` (advanced) | `enabled: false` (manually enabled) |

### 6.3 Anti-corrosion layer (ACL)

Kata and E2B behind `Sandbox` SPI can coexist:
- `ProviderSelector` routing by tenant preference + capability (GPU requirements → Kata)
- Zero changes in switching: the caller only relies on the `Sandbox` interface and is not aware of the underlying implementation
- Internal processing of each Adapter: RuntimeClass creation, image pulling, network policy injection

### 6.4 ProviderSelector routing rules

| Condition | Routing Result | Reason |
| --- | --- | --- |
| `spec.GPU > 0` | Force `kata` | E2B does not support GPU passthrough |
| `tenant.sandboxProvider = "e2b"` and `spec.GPU = 0` | `e2b` | Tenant Preferences |
| `tenant.sandboxProvider = "kata"` or not set | `kata` | Default main force |

### 6.5 Pool model implementation

Independent bucketing for each `SandboxSpec` dimension (runtime/CPU/mem/image):

```
pool["kata:1:512Mi:python"]   = make(chan SandboxHandle, maxIdlePerSpec)
pool["kata:2:1Gi:python"]     = make(chan SandboxHandle, maxIdlePerSpec)
pool["e2b:0.5:256Mi:node"]    = make(chan SandboxHandle, maxIdlePerSpec)
```

- `Acquire`: fetch from queue → create new one if none → return handle
- `Release`: return the queue → refresh TTL → destroy it directly if it exceeds `maxIdlePerSpec`
- TTL expiration: background goroutine regularly scans and cleans up expired idle sandboxes

### 6.6 Stage introduction strategy

| Stage | Configuration File | Adapter Status | Description |
| --- | --- | --- | --- |
| 1~3 | starter / standard | Do not deploy all | `optional_disabled` in the list |
| four | advanced | `KataAdapter` enabled | `kata.enabled: true` |
| Four | full | `KataAdapter` + `E2BAdapter` | Dual Adapters coexist |

When not enabled, the entire repository will not be compiled (Wire DI is excluded), with zero impact.

### 6.7 Dependency graph

```
code class Tool (ai-tool-registry, kind=code)
  → SandboxExecutor (ai-sandbox-manager)
    → Sandbox Pool (Main repository)
      → ProviderSelector (Main repository)
        → KataAdapter (Main repository) → K8s RuntimeClass=kata → Kata Pod
        → E2BAdapter (Main repository)  → E2B SDK/API → Firecracker microVM
          ← depends → Redis (Pool metadata) + Keycloak (tenant)
```

### 6.8 K8s node planning

| Node Group | RuntimeClass | Purpose | Phase |
| --- | --- | --- | --- |
| `KATA_N` | `kata` | Code sandbox execution + GPU computing | Phase 4 |
| Regular nodes | `runc` | Control plane (ai-sandbox-manager itself) | Phase 4 |

---

## Request path panorama

```
Agent runtime / code class Tool（through ai-tool-registry register）
  → POST /v1/sandbox/{id}/exec (JWT)
    → ai-sandbox-manager access layer handler [Gin / Hertz]
      → quota/Timeout check + Tenant context（Keycloak JWT → tenant_id）
        → ProviderSelector: Decide kata still e2b
          → sandbox pool: from correspondence spec Bucket gets free handle
            → [warm] → Return directly SandboxHandle（~1s kata / ~150ms e2b）
            → [cold] → New Kata Pod / E2B microVM（~5s / ~2s）
              → injection: code + pip/npm rely + network strategy + environment variables
                → Execute agent running code（inside container localhost Side car agent）
                  → Watchdog monitoring: time out/Resource exceeded → forceKill + Recycle
                  → collect: stdout / stderr / exit_code / resource_usage
                    → audit: sandbox_exec_audit（PG append-only, asynchronous）
                      → Measurement: duration_ms / cpu_ms / mem_bytes / gpu_ms
                        → Release: return pool（warm）or destroyed directly（time out/abnormal）
                          → return ExecResult → Agent

SandboxPool Backstage:
  → TTL Scanner goroutine: Every 30s Scan the free pool
    → Exceed TTL idle sandbox → destroy Pod → tmpfs clean up
  → Pool capacity check: idle ≤ maxIdlePerSpec
```

---

> **Associated documents**: This repository `docs/DESIGN.md` · `docs/SKILLS.md` · `docs/SPECS.md`
> **Architecture Reference**: §4.3.3 (Sandbox Execution Environment) · §9.1 (Sandbox Node Group) · §10.3 (Sandbox SPI) · §10.4 (SPI Multiple Implementation) · §10.6 (Component Registry/Tool→SandboxExecutor) · §15.5 (DDD Layering) · §16 (BOM)
