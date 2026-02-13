# Kernel Scope

本文档定义 `kernel` 作为通用 Agent 内核的职责边界，避免产品行为持续下沉到内核层。

## In Scope（属于 kernel）

- 运行时编排：
  - 单次 run 生命周期管理（running/completed/failed/interrupted/waiting_approval）
  - session single-flight、并发冲突治理
  - 事件持久化与历史恢复
- 抽象契约：
  - `model.LLM`、`tool.Tool`、`policy.Hook`、`plugin.Registry`
  - 统一错误码与可机读状态
- 执行环境抽象：
  - `PermissionMode`、`ExecutionRoute`、sandbox backend 接口与探测
  - 审批接口（Approver）与错误约定
- 可插拔机制：
  - provider 生命周期（Init/Start/Stop）
  - 运行期可扩展 tool/policy provider 组装

## Out of Scope（不属于 kernel）

- 交互产品策略：
  - CLI 输入输出渲染、审批提示文案、slash command UX
  - Web/API 的会话展示风格与页面逻辑
- 产品默认模板与路径策略：
  - `~/.{app}/...` 目录结构、默认提示词文件落盘策略
- 业务域规则：
  - 针对某个具体行业/场景的硬编码 prompt 规则

## 当前仍需治理的边界问题

- 运行时压缩策略仍包含固定中文摘要提示词，属于产品策略混入内核。
- policy 仍以 hook error 短路为主，缺少统一决策对象（allow/deny/require_approval）。
- `session.Store` 仅支持全量 `ListEvents`，缺少游标接口，导致内核可扩展性不足。

## 迁移原则

- 默认规则上移：能由 CLI/API/Web 决定的策略，不在 kernel 默认硬编码。
- 内核只保留契约：保留稳定接口、状态机和错误码，不绑定特定产品文案。
- 兼容优先级：当前阶段允许受控 break（v0.x），但必须提供迁移说明和测试覆盖。
