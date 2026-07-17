# ai-sandbox-manager

> 本文件由元仓 `openstrata-meta/template/app-skeleton` **自动实例化**生成，是 OpenStrata 多仓（polyrepo）体系中的一员。结构遵循架构文档 v2.8 §15.7.2。

- **语言**：Go（主流框架：Gin + Cobra + Wire（热路径 Hertz / go-zero））
- **领域（domain）**：agent-infra
- **可选性**：可选（optional）
- **元仓引用**：`openstrata-meta/repos.yaml`（tag `v1.4.0`）· BOM 见 `openstrata-meta/bom.yaml`

## 仓库统一结构（§15.7.2）

```
ai-sandbox-manager/
├── src/ 或 cmd/            # 业务代码（按 §15.6.2 DDD 四层）
├── infrastructure/config/  # ★ 本仓 SPI 适配器局部配置片段
├── Dockerfile / helm/      # 部署产物
├── .github/                # 每仓独立 CI（build/test/scan/publish）
├── arch/                   # ★ 架构定位（本仓在分层中的角色/边界）
├── design/                 # ★ 设计规则 + ADR（演化式 AI 编码准则）
├── skills/                 # ★ AI 编码技能（供 CodeBuddy/Cursor 等消费）
└── specs/                  # ★ 规范与契约（API/AgentSpec/SPI Schema）
```

## 本仓职责（TODO：补全）

用 1–3 句描述：本仓在分层架构中的角色、对外暴露 / 依赖的 **SPI 端口**、所依赖外部开源组件（默认 ✅ / 备选）。

## 本地开发（TODO：补全）

- 构建 / 测试 / 运行命令
- 如何接入元仓 `dependencies/` 依赖图与 `profiles/` 预制

> 演化式 AI 编码：本仓的 `arch/ design/ skills/ specs/` 是 AI 助手与贡献者共同遵守的事实源；新决策以 ADR 沉淀于 `design/adr/`。
