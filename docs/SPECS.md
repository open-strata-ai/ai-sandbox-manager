# ai-sandbox-manager · Specifications

> **Specification layer** — API/CLI interface, data model, deployment configuration
> **Source document**: docs/DESIGN.md §7 / ​​§8 / §11
> **Platform version**: v1.0.0

---

## 7. API / CLI interface

### 7.1 HTTP API

| Method | Path | Description | Authentication |
| --- | --- | --- | --- |
| POST | `/v1/sandbox/acquire` | Acquire sandbox from pool | Keycloak JWT |
| POST | `/v1/sandbox/{id}/exec` | Execute code in the specified sandbox | Keycloak JWT |
| POST | `/v1/sandbox/{id}/release` | Return/destroy sandbox | Keycloak JWT |
| GET | `/v1/sandbox/pool/stats` | Query pool status | Keycloak JWT |
| GET | `/healthz` | Liveness/readiness probe | None |
| GET | `/metrics` | Prometheus metrics | Intranet |

### 7.2 API request/response model

#### Acquire request/response

**Request body (JSON)**:
```json
{
  "runtime": "kata",
  "cpu": "1",
  "memory": "512Mi",
  "gpu": 0,
  "network": "deny-all",
  "timeout_ms": 30000,
  "image": "python:3.11-slim",
  "ttl": 300
}
```

**Response body (JSON)**:
```json
{
  "handle": {
    "id": "sb-a1b2c3",
    "runtime": "kata",
    "endpoint": "http://10.0.3.5:9090",
    "leased_at": 1721222400
  }
}
```

#### Execute request/response

**Request body (JSON)**:
```json
{
  "code": "print(sum(range(100)))",
  "language": "python",
  "deps": ["numpy==1.26.0"],
  "args": [],
  "env": {"PYTHONUNBUFFERED": "1"}
}
```

**Response body (JSON)**:
```json
{
  "stdout": "4950\n",
  "stderr": "",
  "exit_code": 0,
  "duration_ms": 42,
  "resource_usage": {
    "cpu_ms": 35,
    "mem_bytes": 41943040,
    "gpu_ms": 0
  }
}
```

#### Pool Stats Response

```json
{
  "pools": {
    "kata:1:512Mi:python": {
      "idle": 3,
      "active": 5,
      "max_idle": 4
    },
    "e2b:0.5:256Mi:node": {
      "idle": 1,
      "active": 2,
      "max_idle": 4
    }
  },
  "total_active": 7,
  "total_idle": 4
}
```

### 7.3 CLI

This repository does not publish a separate CLI; `aictl` can indirectly manage sandbox policies through the control plane. Operation and maintenance is started with `--config`.

---

## 8. Data model

### 8.1 Persistence Overview

This repository is mainly based on **runtime state**, with light persistence:

| Storage | Role | Data Content |
| --- | --- | --- |
| Redis (core) | Runtime status | Pool metadata (idle/active handles), quota count |
| PostgreSQL (core) | Audit | `sandbox_exec_audit` (execute audit) |
| MinIO (optional) | Product storage | Execute product implementation (requires explicit enablement + tenant isolation) |

> Do not store user code long-term (security).

### 8.2 Core table: `sandbox_exec_audit`

```sql
CREATE TABLE sandbox_exec_audit (
  id             BIGSERIAL PRIMARY KEY,
  tenant_id      TEXT,
  runtime        TEXT,
  exit_code      INT,
  duration_ms    INT,
  resource_usage JSONB,
  created_at     TIMESTAMPTZ DEFAULT now()
);
```

**Column Description**:

| Column | Type | Description |
| --- | --- | --- |
| id | BIGSERIAL | Auto-increment primary key |
| tenant_id | TEXT | Tenant ID |
| runtime | TEXT | Runtime used: `kata` / `e2b` |
| exit_code | INT | Process exit code; -1 = timeout/killed |
| duration_ms | INT | Execution duration (milliseconds) |
| resource_usage | JSONB | `{cpu_ms, mem_bytes, gpu_ms}` |
| created_at | TIMESTAMPTZ | Audit timestamp |

> Note: This table does not store the code body (security considerations).

### 8.3 Redis key design

| Key Pattern | Purpose | TTL |
| --- | --- | --- |
| `sandbox:pool:{spec_hash}` | List of free sandbox handles | None (Acquire/Release maintenance) |
| `sandbox:active:{handle_id}` | Active sandbox metadata | Consistent with the lease |
| `sandbox:quota:{tenant}:count` | Tenant concurrent sandbox count | None (decrease when Release) |
| `sandbox:quota:{tenant}:cpu` | Tenant CPU quota count | None |

---

## 11. Configuration and deployment

### 11.1 Deployment form

| Properties | Values ​​|
| --- | --- |
| Required | optional (not deployed by default) |
| Activation stage | Advanced gear starts to light up |
| namespace | `ai-system` or tenant namespace |
| Deployment method | K8s Deployment (advanced+) |
| Sandbox Nodes | Requires `kata-containers` RuntimeClass (§9.1 Sandbox Node Group) |

### 11.2 K8s resource configuration

**Control surface (ai-sandbox-manager itself)**:
```yaml
resources:
  requests:
    cpu: 250m
    memory: 256Mi
  limits:
    cpu: 1
    memory: 1Gi
```

**Sandbox Pod** (resources are dynamically determined by `SandboxSpec`):
```yaml
# Example: Python code execution sandbox
resources:
  requests:
    cpu: "1"
    memory: "512Mi"
  limits:
    cpu: "1"
    memory: "512Mi"
```

### 11.3 Probe configuration

| probe | path | description | initialDelaySeconds | periodSeconds |
| --- | --- | --- | --- | --- |
| Alive | `GET /healthz` | Quick return 200 | 5 | 10 |
| Ready | `GET /healthz` | Verify Redis + ≥1 provider healthy | 5 | 10 |

### 11.4 Rolling update strategy

```yaml
strategy:
  type: RollingUpdate
```

Multi-copy management plane + probe. Sandbox Pods do not scroll with the management plane.

### 11.5 Complete list of configuration keys

**File location**: `infrastructure/config/`

```yaml
sandbox:
  enabled: true                  #Component master switch

  pool:
    maxIdlePerSpec: 4            #Maximum idle number of each specification
    ttlSeconds: 300              #Idle collection TTL

  defaults:
    runtime: kata                #Main operation time
    cpu: "1"
    memory: "512Mi"
    network: deny-all            #Default network policy
    timeoutMs: 30000             #Default timeout

  providers:
    kata:
      enabled: true              #Is Kata enabled?
      runtimeClass: kata         #K8s RuntimeClass name
    e2b:
      enabled: false             #E2B is turned off by default (optional)
      apiKeyFrom: vault://e2b # E2B API Key source
```

**Configuration key description**:

| key | type | default value | description |
| --- | --- | --- | --- |
| `sandbox.enabled` | bool | `false` | Sandbox component master switch |
| `sandbox.pool.maxIdlePerSpec` | int | `4` | Maximum number of pre-created idle sandboxes for each specification |
| `sandbox.pool.ttlSeconds` | int | `300` | Idle sandbox recycling time (seconds) |
| `sandbox.defaults.runtime` | string | `kata` | Default runtime (`kata` or `e2b`) |
| `sandbox.defaults.cpu` | string | `1` | Default number of CPU cores |
| `sandbox.defaults.memory` | string | `512Mi` | Default memory |
| `sandbox.defaults.network` | string | `deny-all` | Default network policy |
| `sandbox.defaults.timeoutMs` | int | `30000` | Default execution timeout (milliseconds) |
| `sandbox.providers.kata.enabled` | bool | `true` | Enable Kata adapter |
| `sandbox.providers.kata.runtimeClass` | string | `kata` | Kata’s K8s RuntimeClass name |
| `sandbox.providers.e2b.enabled` | bool | `false` | Enable E2B adapter |
| `sandbox.providers.e2b.apiKeyFrom` | string | `vault://e2b` | E2B API Key Vault path |

### 11.6 Stage introduction strategy

| Stages | Components | Configuration Status |
| --- | --- | --- |
| One to Three (starter/standard) | All | `optional_disabled` — Do not deploy |
| Four (advanced) | Kata + E2B | `sandbox.enabled=true` + profiles remove `optional_disabled` |
| Four (full) | All + GPU | GPU Sandbox + E2B Alternative |

### 11.7 Start and stop control

```yaml
# PlatformManifest stage switch
sandbox:
  enabled: false  #After closing the code class Tool returns "Sandbox is not available"
```

Start and stop with zero downtime: After closing, the rented sandbox can be executed normally until released, and new requests will return 503.

### 11.8 Dependent components

| Component | Type | Required | Description |
| --- | --- | --- | --- |
| Kata Containers | Runtime | optional | VM-level isolation (sandbox node group) |
| E2B | runtime | optional | microVM (alternative, cloud or self-hosted) |
| Redis | cache | core | pool metadata, quota count |
| PostgreSQL | storage | core | audit log |
| Keycloak | authentication | core | tenant identity |

---

## Traceability matrix

| Chapter | Source document DESIGN.md corresponding |
| --- | --- |
| 7 API/CLI/Configuration Interface | §7 |
| 8 Data Model and Storage | §8 |
| 11 Configuration and Deployment | §11 |

> **Change Record**: v0.1 | 2026-07-17 | First draft (extracted from DESIGN.md §7/§8/§11)
