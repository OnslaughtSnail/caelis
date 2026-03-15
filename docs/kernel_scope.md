# Kernel Scope

本文档定义 `kernel` 包的职责边界。kernel 是通用 Agent 运行时基础设施，不绑定任何产品层行为。上层（CLI/Web/API）通过 kernel 的抽象契约组装产品逻辑，不将产品决策下沉到 kernel。

## In Scope

**运行时编排**
- 单次 run 生命周期管理（running / completed / failed / interrupted / waiting_approval）
- session single-flight 与并发冲突治理
- 事件持久化与历史恢复
- delegated child-run 编排与 lineage 元数据

**抽象契约**
- `model.LLM`、`tool.Tool`、`policy.Hook`、`plugin.Registry`
- 统一错误码与可机读状态（`execenv.ErrorCode`）

**执行环境抽象**
- `PermissionMode`、`ExecutionRoute`
- sandbox backend 接口与探测
- 审批接口（`Approver`）与错误约定

**可插拔机制**
- provider 生命周期（Init / Start / Stop）
- 运行期可扩展 tool/policy provider 组装

## Out of Scope

**交互产品策略**
- CLI 输入输出渲染、审批提示文案、slash command UX
- Web/API 的会话展示风格与页面逻辑

**产品默认与路径策略**
- `~/.{app}/...` 目录结构
- 默认提示词文件落盘策略

**业务域规则**
- 针对特定行业/场景的硬编码 prompt 规则

## 设计原则

- **默认规则上移**：能由 CLI/API/Web 决定的策略，不在 kernel 默认硬编码。
- **内核只保留契约**：保留稳定接口、状态机和错误码，不绑定特定产品文案。
- **兼容优先**：当前阶段（v0.x）允许受控 break，但必须提供迁移说明和测试覆盖。

## 已知边界问题

- policy 以 hook error 短路为主，缺少统一决策对象（allow/deny/require_approval）。
- `session.Store` 仅支持全量 `ListEvents`，缺少游标接口。
