# ai-sandbox-manager · Skills & Rules

> **Skill Rules Layer** — executable rules for key algorithms, concurrency/performance models, and security policies
> **Source document**: design/DESIGN.md §5 / §9 / §12
> **Platform version**: v1.0.0

---

## Algorithm rules (§5)

### RULE-SB-001: Sandbox pool life cycle

**Trigger**: `Acquire(spec)` call

**constraint**:
1. Maintain an idle pool bucketed by `SandboxSpec` (runtime/CPU/mem/image)
2. `Acquire` will reuse the warm sandbox first, and create a new one if it is not free.
3. `Release` returns the warm sandbox and refreshes the TTL
4. TTL expiration or pool limit triggers destruction (tmpfs cleanup)
5. Execution timeout/resource overrun will be killed and recycled by the watchdog

**Example**:
```
State:  pool["kata:1:512Mi:python"] = [h1, h2, h3]  // 3 warm
Request: Acquire(CPU="1", Memory="512Mi", Runtime="kata", Image="python")
Action:  Take from the pool h1（warm, ~1s），No new creation
        Pool reduction: [h2, h3]
Return:  SandboxHandle{ID: h1, Endpoint: "10.0.3.5:9090"}

TTL Expire:
State:  h3 idle 300s（TTL=300s）
Action:  Remove from pool h3 → destroy Pod → tmpfs clean up
```

### RULE-SB-002: Pool capacity management

**Trigger**: `Acquire` when the pool is empty / `Release` when the pool is close to the upper limit

**constraint**:
- Pool limit: `maxIdlePerSpec` (default 4)
- The pool is empty and `currently active <node resource limit` → New
- The pool is empty and `currently active ≥ node resource limit` → blocks or returns `429 Busy`
- The pool is full when returned → destroyed directly (without caching)

**Example**:
```
Config:  maxIdlePerSpec=4
State:   pool size=4, all busy (leased)
Request: Acquire → Pool is not free
Check:   node CPU/Mem enough margin → Create a new chapter5indivual
Action:  Kata Pod cold start（~5s）

Config:  maxIdlePerSpec=4, node CPU Full
Request: Acquire → Pool is not free, Node cannot be created
Action:  429 Too Many Requests "sandbox pool exhausted"
```

### RULE-SB-003: Isolation Policy

**Trigger**: Sandbox creation phase

**constraint**:
- **Kata (main)**: VM level isolation, K8s `RuntimeClass=kata`, default `NetworkPolicy: deny-all + allow-same-ns`
- **E2B (Alternative)**: Firecracker microVM, called via SDK, no GPU
- Filesystem: temporary `tmpfs`, cleaned up after execution; no persistence (unless PVC is explicitly mounted, optional)

**Example**:
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
  Result: Network deny-all → curl time out/Connection recircuit breakerd
          Sandbox unchanged（Only the network is unreachable）
```

### RULE-SB-004: Network Policy

**Trigger**: NetworkPolicy is configured when the sandbox is created

**constraint**:
- `deny-all`: disable all outgoing/incoming networks (default)
- `allow-same-ns`: Allow communication within the same namespace
- `egress-allowlist`: only allow whitelist domain names
- Prevent data leakage/abuse

**Example**:
```
SandboxSpec:
  Network: egress-allowlist
  EgressList: ["pypi.org", "files.pythonhosted.org"]

result: pip install accessible pypi
     Any other domain name（like telegram.org）→ Connection timeout
```

### RULE-SB-005: ProviderSelector routing

**Trigger**: Each time `Acquire` is called, decide whether to use Kata or E2B

**constraint**:
1. `spec.GPU > 0` → force Kata (E2B does not support GPU passthrough)
2. Tenant preference `tenant.sandboxProvider` → priority matching
3. Default `sandbox.defaults.runtime` (currently `kata`)

**Example**:
```
Case 1: spec.GPU=1 → Select → "kata" (E2B no GPU)
Case 2: spec.GPU=0, tenant.pref=kata → Select → "kata"
Case 3: spec.GPU=0, tenant.pref=e2b → Select → "e2b"
Case 4: spec.GPU=0, tenant.pref=nil → Select → "kata" (defaults)
```

---

## Concurrency and Performance Rules (§9)

### RULE-SB-006: Pool model concurrency

**Trigger**: `Acquire`/`Release` concurrent calls

**constraint**:
- One `chan SandboxHandle` per `SandboxSpec` bucket to achieve lock-free retrieval
- `Acquire`/`Release` millisecond level
- Concurrency safety: channel natural FIFO, no race conditions

**Example**:
```
Pool Structure:
  pools = map[string]chan SandboxHandle
  pools["kata:1:512Mi:python"] = make(chan SandboxHandle, maxIdlePerSpec)

Acquire: h := <-pools[key]  (Block waiting or return immediately)
Release: pools[key] <- h     (non-blocking send or drop)
```

### RULE-SB-007: Goroutine model

**Trigger**: Each time a request is executed

**constraint**:
- One goroutine + context timeout per `Execute`
- Watchdog goroutine: monitor timeout/resources, timeout triggers forced kill
- Disable waiting for sandbox execution to complete in the main goroutine (must be asynchronous)

**Example**:
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

### RULE-SB-008: Back pressure protection

**Trigger**: The number of concurrent sandboxes approaches the node limit

**constraint**:
- The number of concurrent sandboxes is subject to `maxIdlePerSpec` + the node GPU/CPU upper limit
- `Acquire` blocks or returns `429 Busy` when exceeded
- Protect nodes from OOM / not affecting other Pods

**Example**:
```
Config:  maxIdlePerSpec=4, node CPU=8, Already6active sandboxes each1 CPU
Request: Acquire(CPU="1")
Check:   6+1=7 ≤ 8 → Can create new
Action:  Create a new chapter7sandbox

Request: Acquire(CPU="2")
Check:   6+2=8 ≤ 8 → Can（But borders are not recommended）
Action:  Create a new chapter8sandbox

Request: Acquire (arbitrary)
Check:   8=8 → node CPU Full
Action:  429 Too Many Requests "node capacity exhausted"
```

### RULE-SB-009: Stateless control plane

**Trigger**: Deploy configuration

**constraint**:
- The management plane is stateless and can be expanded horizontally (pool metadata is stored in Redis)
- The sandbox instance exists with the node (does not scroll with the management plane)
- Expanding the management plane without losing the sandbox pool status

---

## Safety Rules (§12)

### RULE-SB-010: VM/microVM level isolation

**Trigger**: Sandbox creation

**constraint**:
- **Kata**: VM-level isolation, independent lightweight VM (KVM) per sandbox
- **E2B**: Firecracker microVM, standalone VM
- Disable container-level isolation (`docker run`) - VM isolation required
- The host cannot be accessed by code within the sandbox

### RULE-SB-011: Network Isolation

**Trigger**: When the sandbox is created

**constraint**:
- Default `NetworkPolicy: deny-all` (forbid all outgoing/incoming networks)
- The outbound whitelist is only open when `egress-allowlist` is explicitly declared
- disallow `allow-all` network policy
- Intranet communication is limited to the same namespace (`allow-same-ns`)

### RULE-SB-012: Resource hard limit

**Trigger**: Sandbox Pod creation

**constraint**:
- CPU/Mem/GPU are restricted by K8s container `resources.limits`
- Hard killing by cgroups (OOMKill/CPU throttle) if the limit is exceeded
- GPU passthrough via device plugin (Kata only)
- Do not allow `limits=requests` to be ignored (must be set explicitly)

### RULE-SB-013: Execution timeout

**Trigger**: Every time `Execute` is called

**constraint**:
- Default timeout 30s (`sandbox.defaults.timeoutMs: 30000`)
- Watchdog forcefully recycles the sandbox after timeout (`forceKill`)
- The timeout sandbox does not return the pool (directly destroys it)
- Timeout record auditing + metering

**Example**:
```
Config:  timeoutMs=30000
Code:    "while True: pass"  (infinite loop)
Action:  After 30s → watchdog detects timeout
         → forceKill(Pod) → tmpfs cleaned
         → ExecResult{ExitCode: -1, DurationMs: 30001, Status: "timeout"}
         → audit + metric recorded
```

### RULE-SB-014: Code is not persistent

**Trigger**: Every execution completion/timeout/exception

**constraint**:
- Temporary `tmpfs`, clean up after execution
- No long-term storage of user code (security)
- Optional: Execution artifact (artifact) implementation MinIO needs to be explicitly enabled and isolated by tenants
- The audit log does not record the code body (only metadata is recorded)

### RULE-SB-015: Full audit

**Trigger**: Each time execution is completed (success/failure/timeout)

**constraint**:
- Audit writes to PostgreSQL `sandbox_exec_audit`
- Fields: tenant_id, runtime, exit_code, duration_ms, resource_usage, created_at
- Do not record the code body
- Asynchronous writing, does not block the main path

---

## Observability rules

- OTel traces + auditing is enabled by default (core)
- Prometheus metrics: pool utilization, Acquire/Execute QPS, execution time (p50/p95/p99), timeout rate, resource consumption
- No OOM / avalanche of monitoring node resources under high-concurrency Execute

---

## Traceability matrix

| Rules | Source Document DESIGN.md |
| --- | --- |
| RULE-SB-001~005 | §5 Key Algorithm |
| RULE-SB-006~009 | §9 Concurrency and Performance |
| RULE-SB-010~015 | §12 Observability/Security |

> **Change Log**: v0.1 | 2026-07-17 | First draft (extracted from DESIGN.md §5/§9/§12)
