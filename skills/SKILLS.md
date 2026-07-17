# ai-sandbox-manager · Skills & Rules

> **技能规则层** — 关键算法、并发/性能模型、安全策略的可执行规则
> **源文档**: design/DESIGN.md §5 / §9 / §12
> **平台版本**: v1.4.0

---

## 算法规则（§5）

### RULE-SB-001: 沙箱池生命周期

**触发**: `Acquire(spec)` 调用

**约束**:
1. 维护按 `SandboxSpec`（runtime/CPU/mem/image）分桶的空闲池
2. `Acquire` 优先复用 warm 沙箱，无空闲则新建
3. `Release` 归还 warm 沙箱并刷新 TTL
4. TTL 到期或池上限触发销毁（tmpfs 清理）
5. 执行超时/资源超限由看门狗强杀并回收

**示例**:
```
State:  pool["kata:1:512Mi:python"] = [h1, h2, h3]  // 3 warm
Request: Acquire(CPU="1", Memory="512Mi", Runtime="kata", Image="python")
Action:  从池取 h1（warm, ~1s），不新建
        池减少: [h2, h3]
Return:  SandboxHandle{ID: h1, Endpoint: "10.0.3.5:9090"}

TTL Expire:
State:  h3 闲置 300s（TTL=300s）
Action:  从池移除 h3 → 销毁 Pod → tmpfs 清理
```

### RULE-SB-002: 池容量管理

**触发**: `Acquire` 时池为空 / `Release` 时池接近上限

**约束**:
- 池上限: `maxIdlePerSpec`（默认 4）
- 池为空且 `当前活跃 < 节点资源上限` → 新建
- 池为空且 `当前活跃 ≥ 节点资源上限` → 阻塞或返回 `429 Busy`
- 归还时池已满 → 直接销毁（不缓存）

**示例**:
```
Config:  maxIdlePerSpec=4
State:   pool size=4, all busy (leased)
Request: Acquire → 池无空闲
Check:   节点 CPU/Mem 余量足够 → 新建第5个
Action:  Kata Pod 冷启动（~5s）

Config:  maxIdlePerSpec=4, 节点 CPU 已满
Request: Acquire → 池无空闲, 节点不可新建
Action:  429 Too Many Requests "sandbox pool exhausted"
```

### RULE-SB-003: 隔离策略

**触发**: 沙箱创建阶段

**约束**:
- **Kata（主力）**: VM 级隔离, K8s `RuntimeClass=kata`, 默认 `NetworkPolicy: deny-all + allow-same-ns`
- **E2B（备选）**: Firecracker microVM, 经 SDK 调用, 无 GPU
- 文件系统: 临时 `tmpfs`, 执行后清理; 无持久化（除非显式挂载 PVC, optional）

**示例**:
```
SandboxSpec:
  Runtime: kata
  Network: deny-all
  Image:   python:3.11-slim

Kata Pod:
  RuntimeClass: kata
  NetworkPolicy: egress deny-all
  Volume: emptyDir(tmpfs) → /workspace
  Limits: cpu=1, memory=512Mi, nvidia.com/gpu=0

Execute Code:
  Code: "import os; os.system('curl http://evil.com')"
  Result: Network deny-all → curl 超时/连接拒绝
          沙箱不变（仅网络不可达）
```

### RULE-SB-004: 网络策略

**触发**: 沙箱创建时配置 NetworkPolicy

**约束**:
- `deny-all`: 禁止所有出网/入网（默认）
- `allow-same-ns`: 允许同命名空间内通信
- `egress-allowlist`: 仅放行白名单域名
- 防止数据外泄/滥用

**示例**:
```
SandboxSpec:
  Network: egress-allowlist
  EgressList: ["pypi.org", "files.pythonhosted.org"]

结果: pip install 可访问 pypi
     任意其他域名（如 telegram.org）→ 连接超时
```

### RULE-SB-005: ProviderSelector 路由

**触发**: 每次 `Acquire` 调用，决定使用 Kata 还是 E2B

**约束**:
1. `spec.GPU > 0` → 强制 Kata（E2B 不支持 GPU 直通）
2. 租户偏好 `tenant.sandboxProvider` → 优先匹配
3. 默认 `sandbox.defaults.runtime`（当前为 `kata`）

**示例**:
```
Case 1: spec.GPU=1 → Select → "kata" (E2B no GPU)
Case 2: spec.GPU=0, tenant.pref=kata → Select → "kata"
Case 3: spec.GPU=0, tenant.pref=e2b → Select → "e2b"
Case 4: spec.GPU=0, tenant.pref=nil → Select → "kata" (defaults)
```

---

## 并发与性能规则（§9）

### RULE-SB-006: 池模型并发

**触发**: `Acquire`/`Release` 并发调用

**约束**:
- 每 `SandboxSpec` 桶一个 `chan SandboxHandle` 实现无锁取还
- `Acquire`/`Release` 毫秒级
- 并发安全: channel 天然 FIFO 无竞态

**示例**:
```
Pool Structure:
  pools = map[string]chan SandboxHandle
  pools["kata:1:512Mi:python"] = make(chan SandboxHandle, maxIdlePerSpec)

Acquire: h := <-pools[key]  (阻塞等待或立即返回)
Release: pools[key] <- h     (非阻塞发送或丢弃)
```

### RULE-SB-007: Goroutine 模型

**触发**: 每次执行请求

**约束**:
- 每个 `Execute` 一个 goroutine + context 超时
- 看门狗 goroutine: 监控超时/资源，超时触发强杀
- 禁止在主 goroutine 中等待沙箱执行完成（必须异步）

**示例**:
```
Execute goroutine:   ctx, cancel := context.WithTimeout(ctx, spec.TimeoutMs)
                     result, err := exec(ctx, handle, req)
                     cancel()

Watchdog goroutine:  <-ctx.Done()
                     if ctx.Err() == context.DeadlineExceeded {
                       forceKill(handle)
                       recordTimeout(handle)
                     }
```

### RULE-SB-008: 背压保护

**触发**: 并发沙箱数接近节点上限

**约束**:
- 并发沙箱数受 `maxIdlePerSpec` + 节点 GPU/CPU 上限约束
- 超出时 `Acquire` 阻塞或返回 `429 Busy`
- 保护节点不 OOM / 不影响其他 Pod

**示例**:
```
Config:  maxIdlePerSpec=4, 节点 CPU=8, 已有6个活跃沙箱各占1 CPU
Request: Acquire(CPU="1")
Check:   6+1=7 ≤ 8 → 可新建
Action:  新建第7个沙箱

Request: Acquire(CPU="2")
Check:   6+2=8 ≤ 8 → 可（但不推荐边界）
Action:  新建第8个沙箱

Request: Acquire (任意)
Check:   8=8 → 节点 CPU 满
Action:  429 Too Many Requests "node capacity exhausted"
```

### RULE-SB-009: 无状态控制面

**触发**: 部署配置

**约束**:
- 管理面无状态可水平扩（池元数据存 Redis）
- 沙箱实例随节点存在（不随管理面滚动）
- 扩容管理面不丢失沙箱池状态

---

## 安全规则（§12）

### RULE-SB-010: VM/microVM 级隔离

**触发**: 沙箱创建

**约束**:
- **Kata**: VM 级隔离，每个沙箱独立轻量 VM（KVM）
- **E2B**: Firecracker microVM，独立 VM
- 禁止容器级隔离（`docker run`）—— 必须 VM 隔离
- 宿主机不可被沙箱内代码访问

### RULE-SB-011: 网络隔离

**触发**: 沙箱创建时

**约束**:
- 默认 `NetworkPolicy: deny-all`（禁止所有出网/入网）
- 出网白名单仅在显式声明 `egress-allowlist` 时开放
- 不允许 `allow-all` 网络策略
- 内网通信仅限同命名空间（`allow-same-ns`）

### RULE-SB-012: 资源硬限制

**触发**: 沙箱 Pod 创建

**约束**:
- CPU/Mem/GPU 经 K8s container `resources.limits` 约束
- 超限由 cgroups 硬杀（OOMKill / CPU throttle）
- GPU 经 device plugin 直通（仅 Kata）
- 不允许 `limits=requests` 忽略（必须显式设置）

### RULE-SB-013: 执行超时

**触发**: 每次 `Execute` 调用

**约束**:
- 默认超时 30s（`sandbox.defaults.timeoutMs: 30000`）
- 超时后看门狗强制回收沙箱（`forceKill`）
- 超时沙箱不归还池（直接销毁）
- 超时记录审计 + 计量

**示例**:
```
Config:  timeoutMs=30000
Code:    "while True: pass"  (infinite loop)
Action:  After 30s → watchdog detects timeout
         → forceKill(Pod) → tmpfs cleaned
         → ExecResult{ExitCode: -1, DurationMs: 30001, Status: "timeout"}
         → audit + metric recorded
```

### RULE-SB-014: 代码不持久化

**触发**: 每次执行完成/超时/异常

**约束**:
- 临时 `tmpfs`，执行后清理
- 不长期存储用户代码（安全）
- 可选: 执行产物（artifact）落地 MinIO 需显式开启且受租户隔离
- 审计日志不记录代码体（只记录 metadata）

### RULE-SB-015: 全量审计

**触发**: 每次执行完成（成功/失败/超时）

**约束**:
- 审计写入 PostgreSQL `sandbox_exec_audit`
- 字段: tenant_id, runtime, exit_code, duration_ms, resource_usage, created_at
- 不记录代码体
- 异步写入，不阻塞主路径

---

## 可观测性规则

- OTel traces + 审计默认开（core）
- Prometheus 指标: 池利用率, Acquire/Execute QPS, 执行时长(p50/p95/p99), 超时率, 资源消耗
- 高并发 Execute 下监控节点资源不 OOM / 不雪崩

---

## 追溯矩阵

| 规则 | 源文档 DESIGN.md |
| --- | --- |
| RULE-SB-001~005 | §5 关键算法 |
| RULE-SB-006~009 | §9 并发与性能 |
| RULE-SB-010~015 | §12 可观测性/安全 |

> **变更记录**: v0.1 | 2026-07-17 | 初稿（从 DESIGN.md §5/§9/§12 提取）
