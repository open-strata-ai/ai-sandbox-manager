# ai-sandbox-manager · Specifications

> **规格层** — API/CLI 接口面、数据模型、部署配置
> **源文档**: design/DESIGN.md §7 / §8 / §11
> **平台版本**: v1.4.0

---

## 7. API / CLI 接口面

### 7.1 HTTP API

| 方法 | 路径 | 说明 | 鉴权 |
| --- | --- | --- | --- |
| POST | `/v1/sandbox/acquire` | 从池中获取沙箱 | Keycloak JWT |
| POST | `/v1/sandbox/{id}/exec` | 在指定沙箱内执行代码 | Keycloak JWT |
| POST | `/v1/sandbox/{id}/release` | 归还/销毁沙箱 | Keycloak JWT |
| GET | `/v1/sandbox/pool/stats` | 查询池状态 | Keycloak JWT |
| GET | `/healthz` | 存活/就绪探针 | 无 |
| GET | `/metrics` | Prometheus 指标 | 内网 |

### 7.2 API 请求/响应模型

#### Acquire 请求/响应

**请求体 (JSON)**:
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

**响应体 (JSON)**:
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

#### Execute 请求/响应

**请求体 (JSON)**:
```json
{
  "code": "print(sum(range(100)))",
  "language": "python",
  "deps": ["numpy==1.26.0"],
  "args": [],
  "env": {"PYTHONUNBUFFERED": "1"}
}
```

**响应体 (JSON)**:
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

#### Pool Stats 响应

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

本仓不单独发布 CLI；`aictl` 可经控制面间接管理沙箱策略。运维用 `--config` 启动。

---

## 8. 数据模型

### 8.1 持久化概述

本仓以**运行时状态**为主，轻持久化：

| 存储 | 角色 | 数据内容 |
| --- | --- | --- |
| Redis（core） | 运行时状态 | 池元数据（空闲/活跃句柄）、配额计数 |
| PostgreSQL（core） | 审计 | `sandbox_exec_audit`（执行审计） |
| MinIO（optional） | 产物存储 | 执行产物落地（需显式开启 + 租户隔离） |

> 不长期存储用户代码（安全）。

### 8.2 核心表: `sandbox_exec_audit`

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

**列说明**:

| 列 | 类型 | 说明 |
| --- | --- | --- |
| id | BIGSERIAL | 自增主键 |
| tenant_id | TEXT | 租户标识 |
| runtime | TEXT | 使用的运行时: `kata` / `e2b` |
| exit_code | INT | 进程退出码; -1 = 超时/被杀 |
| duration_ms | INT | 执行时长（毫秒） |
| resource_usage | JSONB | `{cpu_ms, mem_bytes, gpu_ms}` |
| created_at | TIMESTAMPTZ | 审计时间戳 |

> 注意: 此表不存储代码体（安全考量）。

### 8.3 Redis 键设计

| Key Pattern | 用途 | TTL |
| --- | --- | --- |
| `sandbox:pool:{spec_hash}` | 空闲沙箱句柄列表 | 无（Acquire/Release 维护） |
| `sandbox:active:{handle_id}` | 活跃沙箱元数据 | 与租约一致 |
| `sandbox:quota:{tenant}:count` | 租户并发沙箱计数 | 无（Release 时减） |
| `sandbox:quota:{tenant}:cpu` | 租户 CPU 配额计数 | 无 |

---

## 11. 配置与部署

### 11.1 部署形态

| 属性 | 值 |
| --- | --- |
| 必选性 | optional（默认不部署） |
| 启用阶段 | advanced 档起点亮 |
| 命名空间 | `ai-system` 或租户命名空间 |
| 部署方式 | K8s Deployment（advanced+） |
| 沙箱节点 | 需要 `kata-containers` RuntimeClass（§9.1 沙箱节点组） |

### 11.2 K8s 资源配置

**控制面（ai-sandbox-manager 自身）**:
```yaml
resources:
  requests:
    cpu: 250m
    memory: 256Mi
  limits:
    cpu: 1
    memory: 1Gi
```

**沙箱 Pod**（资源由 `SandboxSpec` 动态决定）:
```yaml
# 示例: Python 代码执行沙箱
resources:
  requests:
    cpu: "1"
    memory: "512Mi"
  limits:
    cpu: "1"
    memory: "512Mi"
```

### 11.3 探针配置

| 探针 | 路径 | 说明 | initialDelaySeconds | periodSeconds |
| --- | --- | --- | --- | --- |
| 存活 | `GET /healthz` | 快速返回 200 | 5 | 10 |
| 就绪 | `GET /healthz` | 校验 Redis + ≥1 provider healthy | 5 | 10 |

### 11.4 滚动更新策略

```yaml
strategy:
  type: RollingUpdate
```

多副本管理面 + 探针。沙箱 Pod 不随管理面滚动。

### 11.5 配置键完整列表

**文件位置**: `infrastructure/config/`

```yaml
sandbox:
  enabled: true                  # 组件总开关

  pool:
    maxIdlePerSpec: 4            # 每种规格最大空闲数
    ttlSeconds: 300              # 空闲回收 TTL

  defaults:
    runtime: kata                # 主力运行时
    cpu: "1"
    memory: "512Mi"
    network: deny-all            # 默认网络策略
    timeoutMs: 30000             # 默认超时

  providers:
    kata:
      enabled: true              # Kata 是否启用
      runtimeClass: kata         # K8s RuntimeClass 名称
    e2b:
      enabled: false             # E2B 默认关闭（optional 备选）
      apiKeyFrom: vault://e2b    # E2B API Key 来源
```

**配置键说明**:

| 键 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `sandbox.enabled` | bool | `false` | 沙箱组件总开关 |
| `sandbox.pool.maxIdlePerSpec` | int | `4` | 每种规格最大预创建空闲沙箱数 |
| `sandbox.pool.ttlSeconds` | int | `300` | 空闲沙箱回收时间（秒） |
| `sandbox.defaults.runtime` | string | `kata` | 默认运行时（`kata` 或 `e2b`） |
| `sandbox.defaults.cpu` | string | `1` | 默认 CPU 核心数 |
| `sandbox.defaults.memory` | string | `512Mi` | 默认内存 |
| `sandbox.defaults.network` | string | `deny-all` | 默认网络策略 |
| `sandbox.defaults.timeoutMs` | int | `30000` | 默认执行超时（毫秒） |
| `sandbox.providers.kata.enabled` | bool | `true` | 启用 Kata 适配器 |
| `sandbox.providers.kata.runtimeClass` | string | `kata` | Kata 的 K8s RuntimeClass 名称 |
| `sandbox.providers.e2b.enabled` | bool | `false` | 启用 E2B 适配器 |
| `sandbox.providers.e2b.apiKeyFrom` | string | `vault://e2b` | E2B API Key Vault 路径 |

### 11.6 阶段引入策略

| 阶段 | 组件 | 配置状态 |
| --- | --- | --- |
| 一~三（starter/standard） | 全部 | `optional_disabled` — 不部署 |
| 四（advanced） | Kata + E2B | `sandbox.enabled=true` + profiles 移除 `optional_disabled` |
| 四（full） | 全部 + GPU | GPU 沙箱 + E2B 备选 |

### 11.7 启停控制

```yaml
# PlatformManifest 级开关
sandbox:
  enabled: false  # 关闭后代码类 Tool 返回「沙箱不可用」
```

零停机启停：关闭后已租用沙箱可正常执行至释放，新请求返回 503。

### 11.8 依赖组件

| 组件 | 类型 | 必选 | 说明 |
| --- | --- | --- | --- |
| Kata Containers | 运行时 | optional | VM 级隔离（沙箱节点组） |
| E2B | 运行时 | optional | microVM（备选，云或自托管） |
| Redis | 缓存 | core | 池元数据、配额计数 |
| PostgreSQL | 存储 | core | 审计日志 |
| Keycloak | 认证 | core | 租户身份 |

---

## 追溯矩阵

| 章节 | 源文档 DESIGN.md 对应 |
| --- | --- |
| 7 API/CLI/配置接口面 | §7 |
| 8 数据模型与存储 | §8 |
| 11 配置与部署 | §11 |

> **变更记录**: v0.1 | 2026-07-17 | 初稿（从 DESIGN.md §7/§8/§11 提取）
