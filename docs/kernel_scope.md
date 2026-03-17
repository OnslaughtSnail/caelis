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

## 包结构规则

kernel 下的包应分成三类，避免为了绕循环引用把一段实现拆成新的零碎顶层包。

**稳定契约包**
- `agent`
- `model`
- `tool`
- `policy`
- `session`
- `task`
- `plugin`
- `delegation`

这些包承载协议、核心类型和稳定接口，默认不依赖上层交付逻辑。

**编排与内建实现包**
- `runtime`
- `execenv`
- `llmagent`

这些包可以依赖契约包，但不应把纯 UI、纯产品策略或协议适配细节带进来。

**共享投影/流包**
- `sessionstream`
- `taskstream`

只有在逻辑确实被多个核心包复用，且表达的是稳定概念时，才应保留这类共享包。

## 新包准入规则

- 不要仅因为 import cycle 就新建顶层包；优先在原包内拆成多文件。
- 只有当一个抽象被两个及以上核心子域复用，且语义稳定，才拆成独立包。
- `*stream`、`*view`、`*cap` 这类名字默认是高风险碎包；如果只服务单一主包，应回收到主包中。
- helper 若只被 `runtime` 使用，应放在 `kernel/runtime/*.go`，而不是再拆新的 `kernel/<helper>` 包。
- helper 若只被 `tool` 使用，应放在 `kernel/tool/*.go` 或其 builtin 子目录。

## 当前规整方向

- `runtime`、`session`、`task`、`tool`、`policy`、`execenv`、`plugin`、`model` 这些主干包应继续保留，它们对应真实边界，不是为了避循环硬拆出来的。
- `bootstrap`、`promptpipeline`、`skills` 不再属于 `kernel`。它们分别是应用装配、提示词拼装和技能元数据发现，应放在 `internal/app/...` 这类产品基础设施层。
- `sessionstream` 和 `taskstream` 继续分开保留。前者表达原始 session event live stream，后者表达 task UI update；语义不同，不应合并。
- `eventview` 已回收到 `session`；后续如再出现类似“只提供 session 投影 helper”的顶层包，默认拒绝。
- `toolcap` 已下沉到 `tool/capability`；能力声明可以保留子包，但不再占用 `kernel` 顶层命名空间。
- 今后若再出现类似 `kernel/foohelper`、`kernel/barview` 的新包，默认先拒绝，除非能说明它是稳定共享概念，而不是拆文件的替代品。

## 已知边界问题

- policy 以 hook error 短路为主，缺少统一决策对象（allow/deny/require_approval）。
- `session.Store` 基础接口仍以全量 `ListEvents` 为主；`CursorStore` 扩展接口（`ListEventsAfter`）已在 `filestore` 和 `inmemory` 中实现。
