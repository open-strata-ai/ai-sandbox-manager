# ai-sandbox-manager · Architecture（架构总览）

> **摘自** `design/DESIGN.md` §1 定位与边界 · §2 职责清单 · §3 核心接口 · §6 适配器
> **语言·框架**: Go · Gin + Cobra + Wire（DDD 四层；沙箱池/调度热路径可上 Hertz/go-zero）
> **领域**: agent-infra（Agent 基础设施层 · 沙箱执行环境）
> **optional**: true（可选 · optional，默认关；仅允许代码执行的 Agent 需要）
> **平台版本**: v1.4.0

---

## §1 定位与边界（Scope）

### 1.1 一句话定位

`ai-sandbox-manager` 是 OpenStrata 的**沙箱执行环境管理器**，承载架构 §4.3.3「沙箱执行环境」。它为 Agent 提供**隔离、受限、可回收**的代码执行环境，屏蔽底层 Kata Containers / E2B 的差异，对上层（Agent 运行时经 `ToolRegistry` 注册的代码类 Tool）暴露统一的 `Sandbox` SPI（§10.3）。

### 1.2 解决的核心问题

把"代码在隔离环境里跑"这件危险的事，变成"声明式、带配额、带超时、可池化、可审计"的安全能力，且不绑定某一种沙箱实现（Kata / E2B 可并存切换）。

### 1.3 可选性与适用场景

- **optional**: 默认关（§10.2「沙箱执行 可选（默认关）」）
- **启用条件**: 仅允许代码执行的 Agent 需要本仓
- **阶段引入**: 阶段一~三 starter/standard 不启用（`profiles/optional_disabled` 含 `ai-sandbox-manager`、`kata-containers`、`e2b`），从 advanced 档起点亮
- **可省略场景**: 不需要代码执行的 Agent（如纯问答、API 调用）

### 1.4 架构角色

| 维度 | 说明 |
| --- | --- |
| **层次** | DDD 四层：`domain/`（Sandbox Port + 池模型实体）· `application/`（池调度/执行用例）· `infrastructure/`（KataAdapter/E2BAdapter/Wire DI）· `interfaces/`（Gin handler） |
| **管理面框架** | Gin（沙箱 API） |
| **热路径框架** | Hertz/go-zero（可选，`Acquire`/`Execute` 高频） |
| **DI 方案** | Wire（编译期依赖注入） |
| **沙箱运行时** | Kata Containers（VM 级，主力）/ E2B Firecracker（microVM，备选） |

### 1.5 与其他 Go 组件的分工

| 组件 | 关系类型 | 说明 |
| --- | --- | --- |
| `ai-tool-registry` | 间接依赖（被调用方） | 代码类 Tool 经 tool-registry 注册；运行时本仓作为 `SandboxExecutor` 承载其执行（§10.6 依赖规则 `Tool→SandboxExecutor`）。 |
| `ai-gateway-core` | 独立数据面 | 网关负责"模型调用"，不执行代码；二者数据面独立。 |
| `ai-provisioning-engine` | 部署编排 | 本仓是被部署的 optional 组件之一；其 K8s RuntimeClass / E2B 凭证由装配引擎注入。 |

### 1.6 边界约束

| 约束 | 说明 |
| --- | --- |
| **不注册工具** | 工具有 `ai-tool-registry` 管理，本仓只接收 `ExecRequest` 并执行 |
| **不做模型调用** | 模型调用由 `ai-gateway-core` 负责 |
| **不持久化代码** | 用户代码仅存临时 tmpfs，执行后清理 |
| **不跨租户共享沙箱** | 每个 `Acquire` 返回独立沙箱，不可复用 |
| **默认无网络** | `NetworkPolicy: deny-all`，防数据外泄 |

---

## §2 职责清单

### 2.1 完整职责表

| # | 职责 | 必选/可选 | 触发条件 | 说明 |
| --- | --- | --- | --- | --- |
| R1 | **沙箱生命周期管理** | optional | 开启后始终运行 | 创建/执行/回收；池化复用（§4.3.3 POOL） |
| R2 | **隔离策略** | optional | 沙箱创建时 | VM/microVM 级隔离、网络隔离、资源限制（§4.3.3） |
| R3 | **Kata 适配器** | optional | `providers.kata.enabled: true` | Kata Containers RuntimeClass（主力，§4.3.3） |
| R4 | **E2B 适配器** | optional | `providers.e2b.enabled: true` | E2B Firecracker microVM（备选，§4.3.3） |
| R5 | **配额/超时** | optional | 每次执行请求 | CPU/Mem/GPU、执行超时、网络出网策略 |
| R6 | **依赖注入执行** | optional | `Execute` 调用 | 代码 + 依赖 + 超时 执行请求（§4.3.3 REQ） |
| R7 | **计量/审计** | optional | 每次执行完成 | 执行次数、时长、资源消耗、结果（§4.8 审计） |

### 2.2 职责分级

| 级别 | 职责编号 | 说明 |
| --- | --- | --- |
| **核心路径** | R1, R2, R5, R6 | 沙箱执行的最小闭环 |
| **适配器** | R3, R4 | 二选一或并存（ProviderSelector 路由） |
| **可观测性** | R7 | 计量/审计/追踪 |

### 2.3 运行时保障

| 保障项 | 说明 |
| --- | --- |
| **隔离保证** | VM 级隔离（Kata KVM / E2B Firecracker），非容器级 |
| **资源硬限** | K8s container limits（CPU/Mem/GPU）+ cgroups 强制 |
| **超时强杀** | 看门狗 goroutine 监控，超时强制回收 |
| **自动清理** | tmpfs 清理，无残留代码/数据 |
| **池化复用** | warm 沙箱复用降低冷启动延迟 |

---

## §3 核心接口与抽象

### 3.1 设计原则

领域层定义 `Sandbox` Port（bom.yaml `interface_versions.Sandbox = 1.0.0`）。本仓自身是实现方，对上层 Agent 暴露统一接口，对下适配 Kata/E2B 差异。

### 3.2 Sandbox SPI（v1.0.0）

```go
package domain

// ===== Sandbox SPI（interface_versions.Sandbox = 1.0.0）=====
type Sandbox interface {
    // Acquire 从池中获取/新建一个空闲沙箱
    Acquire(ctx context.Context, spec SandboxSpec) (SandboxHandle, error)
    // Execute 在指定沙箱内执行代码，返回 stdout/stderr/退出码
    Execute(ctx context.Context, h SandboxHandle, req ExecRequest) (ExecResult, error)
    // Release 归还/销毁沙箱
    Release(ctx context.Context, h SandboxHandle) error
    // Health 探活
    Health(ctx context.Context) HealthStatus
}

type SandboxSpec struct {
    Runtime   string        // kata | e2b（由 ProviderSelector 选，§10.4）
    CPU       string        // 如 "1"
    Memory    string        // 如 "512Mi"
    GPU       int           // 0 = 无；Kata 支持直通（§4.3.3）
    Network   NetworkPolicy // deny-all | allow-same-ns | egress-allowlist
    TimeoutMs int
    Image     string        // 运行时镜像（Python/Node/Shell）
    TTL       int           // 空闲回收时间秒数
}

type ExecRequest struct {
    Code     string            // 源码
    Language string            // python|node|shell
    Deps     []string          // pip/npm 依赖
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
    Endpoint string // 容器内执行代理地址
    LeasedAt int64  // Unix timestamp
}

type ResourceUsage struct {
    CPUms    int
    MemBytes int
    GPUms    int
}

// NetworkPolicy 沙箱网络策略类型
type NetworkPolicy string
const (
    NetworkDenyAll      NetworkPolicy = "deny-all"
    NetworkAllowSameNS  NetworkPolicy = "allow-same-ns"
    NetworkEgressAllow  NetworkPolicy = "egress-allowlist"
)
```

### 3.3 ProviderSelector

```go
// ===== ProviderSelector（领域层）=====
type ProviderSelector interface {
    Select(spec SandboxSpec, tenantID string) string // "kata" | "e2b"
}
```

### 3.4 池模型接口

```go
// ===== SandboxPool（领域层，infrastructure 层实现）=====
type SandboxPool interface {
    // TryAcquire 从指定 spec 的池中获取一个空闲沙箱（毫秒级），无空闲则返回 nil
    TryAcquire(specHash string) (*SandboxHandle, bool)
    // PutBack 归还沙箱到池中
    PutBack(specHash string, h *SandboxHandle) error
    // Stats 返回池状态
    Stats() map[string]PoolBucketStats
}

type PoolBucketStats struct {
    Idle    int
    Active  int
    MaxIdle int
}
```

### 3.5 延迟预算

| 操作 | warm 延迟 | cold 延迟 | 说明 |
| --- | --- | --- | --- |
| `Acquire`（Kata） | ~1s | ~5s | 复用预热 Pod vs 新建 |
| `Acquire`（E2B） | ~150ms | ~2s | 复用 microVM vs 新建 |
| `Release`（归还池） | ≤50ms | — | 仅刷新 TTL，不涉及 Pod 操作 |
| `Execute` | 取决于代码 | — | 受 `SandboxSpec.TimeoutMs` 硬约束 |

---

## §6 适配器与 SPI 生态

### 6.1 SPI 端口矩阵

| SPI 端口 | 版本 | 本仓角色 | 外部组件（bom.yaml） | 默认 | 备选 | Adapter |
| --- | --- | --- | --- | --- | --- | --- |
| `Sandbox` | 1.0.0 | 实现方 | Kata Containers（optional）· E2B（optional） | 备选 | 备选 | `KataAdapter` / `E2BAdapter` |
| `Cache` | 1.0.0 | 消费方 | Redis（core） | ✅ | — | 池元数据、配额计数 |
| `Auth` | 1.0.0 | 消费方 | Keycloak（core） | ✅ | — | 租户 JWT 身份 |
| `Tracing` | 1.0.0 | 消费方 | Langfuse/OTel（optional/core） | ✅ | — | 执行链路追踪 |

### 6.2 Sandbox Adapter 对比

| 维度 | KataAdapter | E2BAdapter |
| --- | --- | --- |
| **隔离级别** | VM 级（独立 KVM） | microVM（Firecracker） |
| **GPU 支持** | 支持（NVIDIA device plugin 直通） | 不支持 |
| **warm 启动** | ~1s（复用 Pod） | ~150ms（复用 microVM） |
| **cold 启动** | ~5s（新建 Pod） | ~2s（新建 microVM） |
| **资源限制** | K8s limits + cgroups | E2B SandboxTemplate |
| **网络策略** | K8s NetworkPolicy | E2B 防火墙规则 |
| **文件系统** | tmpfs（emptyDir） | tmpfs（内置） |
| **适用场景** | CPU + GPU 计算、数据科学 | 纯代码执行、快速场景 |
| **部署要求** | K8s RuntimeClass `kata` | E2B 云/SDK 或自托管 |
| **默认状态** | `enabled: true`（advanced） | `enabled: false`（手动开启） |

### 6.3 防腐层（ACL）

`Sandbox` SPI 背后 Kata 与 E2B 可并存：
- `ProviderSelector` 按租户偏好 + 能力路由（GPU 需求 → Kata）
- 切换零改动：调用方只依赖 `Sandbox` 接口，不感知底层实现
- 每个 Adapter 内部处理：RuntimeClass 创建、镜像拉取、网络策略注入

### 6.4 ProviderSelector 路由规则

| 条件 | 路由结果 | 原因 |
| --- | --- | --- |
| `spec.GPU > 0` | 强制 `kata` | E2B 不支持 GPU 直通 |
| `tenant.sandboxProvider = "e2b"` 且 `spec.GPU = 0` | `e2b` | 租户偏好 |
| `tenant.sandboxProvider = "kata"` 或未设置 | `kata` | 默认主力 |

### 6.5 池模型实现

每 `SandboxSpec` 维度（runtime/CPU/mem/image）独立分桶：

```
pool["kata:1:512Mi:python"]   = make(chan SandboxHandle, maxIdlePerSpec)
pool["kata:2:1Gi:python"]     = make(chan SandboxHandle, maxIdlePerSpec)
pool["e2b:0.5:256Mi:node"]    = make(chan SandboxHandle, maxIdlePerSpec)
```

- `Acquire`：从队列取 → 无则新建 → 返回 handle
- `Release`：归还队列 → 刷新 TTL → 超过 `maxIdlePerSpec` 则直接销毁
- TTL 到期：后台 goroutine 定时扫描，清理过期空闲沙箱

### 6.6 阶段引入策略

| 阶段 | 配置档 | Adapter 状态 | 说明 |
| --- | --- | --- | --- |
| 一~三 | starter / standard | 全部不部署 | `optional_disabled` 列表中 |
| 四 | advanced | `KataAdapter` 启用 | `kata.enabled: true` |
| 四 | full | `KataAdapter` + `E2BAdapter` | 双 Adapter 并存 |

不启用时整个仓不编译（Wire DI 排除），零影响。

### 6.7 依赖关系图

```
代码类 Tool (ai-tool-registry, kind=code)
  → SandboxExecutor (ai-sandbox-manager)
    → Sandbox Pool (本仓)
      → ProviderSelector (本仓)
        → KataAdapter (本仓) → K8s RuntimeClass=kata → Kata Pod
        → E2BAdapter (本仓)  → E2B SDK/API → Firecracker microVM
          ← depends → Redis (池元数据) + Keycloak (租户)
```

### 6.8 K8s 节点规划

| 节点组 | RuntimeClass | 用途 | 阶段 |
| --- | --- | --- | --- |
| `KATA_N` | `kata` | 代码沙箱执行 + GPU 计算 | 阶段四 |
| 常规节点 | `runc` | 控制面（ai-sandbox-manager 自身） | 阶段四 |

---

## 请求路径全景

```
Agent 运行时 / 代码类 Tool（经 ai-tool-registry 注册）
  → POST /v1/sandbox/{id}/exec (JWT)
    → ai-sandbox-manager 接入层 handler [Gin / Hertz]
      → 配额/超时校验 + 租户上下文（Keycloak JWT → tenant_id）
        → ProviderSelector: 决定 kata 还是 e2b
          → 沙箱池: 从对应 spec 桶取空闲句柄
            → [warm] → 直接返回 SandboxHandle（~1s kata / ~150ms e2b）
            → [cold] → 新建 Kata Pod / E2B microVM（~5s / ~2s）
              → 注入: 代码 + pip/npm 依赖 + 网络策略 + 环境变量
                → 执行代理运行代码（容器内 localhost 侧车代理）
                  → 看门狗监控: 超时/资源超限 → forceKill + 回收
                  → 收集: stdout / stderr / exit_code / resource_usage
                    → 审计: sandbox_exec_audit（PG append-only, 异步）
                      → 计量: duration_ms / cpu_ms / mem_bytes / gpu_ms
                        → Release: 归还池（warm）或直接销毁（超时/异常）
                          → 返回 ExecResult → Agent

SandboxPool 后台:
  → TTL Scanner goroutine: 每 30s 扫描空闲池
    → 超过 TTL 的空闲沙箱 → 销毁 Pod → tmpfs 清理
  → 池容量检查: idle ≤ maxIdlePerSpec
```

---

> **关联文档**: 本仓 `design/DESIGN.md` · `skills/SKILLS.md` · `specs/SPECS.md`
> **架构引用**: §4.3.3（沙箱执行环境）· §9.1（沙箱节点组）· §10.3（Sandbox SPI）· §10.4（SPI多实现）· §10.6（Component Registry/Tool→SandboxExecutor）· §15.6（DDD分层）· §16（BOM）
