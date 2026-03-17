# Refactor Spec

## 1. 文档用途

这是一份给执行型 Agent 使用的重构规格书，不是架构散文。

执行要求：

- 按阶段推进，一次只做一个阶段。
- 默认以“行为不变的结构重构”为主，除非阶段里明确允许改行为。
- 优先同包拆分，再考虑跨包迁移；不要一上来大搬家。
- 如果本文件与 [docs/kernel_scope.md](./kernel_scope.md) 或 [docs/kernel_contract_v1.md](./kernel_contract_v1.md) 冲突，以后两者为准；如确需调整，必须在同一阶段同步更新文档与测试。

## 2. 基于当前代码的事实

以下判断基于仓库当前代码，不基于理想化目标。

### 2.1 核心边界

- `kernel/session/session.go` 定义了 `Session`、`Event`、`Store`，这是运行历史和恢复语义的基础。
- `kernel/runtime/runtime.go` + `kernel/runtime/runner.go` 是实际的 agent orchestration 中枢，负责 run lease、submission、durable replay、overlay、loop detect、compaction、lifecycle event。
- `kernel/runtime/task_manager.go` 负责 bash/delegate 任务控制、任务记录持久化桥接、后台任务恢复接入；它不是主要的 UI 渲染文件，但职责仍然过宽。
- `kernel/runtime/task_recovery.go` 已经承担一部分“进程重启后任务修复”的逻辑，说明任务恢复语义已经是 kernel runtime 的一部分。
- `kernel/sessionstream/sessionstream.go` 提供原始 session event 的 live stream。
- `kernel/taskstream/taskstream.go` 提供 task 的 UI-facing stream metadata，它和 `sessionstream` 不是同一个层级。
- `kernel/plugin/plugin.go` + 应用装配层（当前为 `internal/app/assembly`）已经形成了稳定的 provider 注册与装配入口。

### 2.2 适配层事实

- `cmd/cli/main.go` 负责 CLI 入口、flag 解析、执行环境、provider 注册、MCP 管理、model factory 装配等，组装责任偏重。
- `cmd/cli/acp.go` 也做了大量与 CLI 相似的组装工作，存在可收敛的重复。
- `internal/acp/server.go` 目前是 ACP 服务端主实现，集中了 RPC 分发、认证、session state、prompt 输入处理、tool/task update 投影、结果清洗、session config 持久化等职责。
- `internal/acp/runtime.go` 已经提供 ACP 文件系统/终端桥接，但 ACP 相关能力仍主要停留在 `internal/acp` 适配层，而不是 kernel 级协议抽象。
- TUI 已经不是单文件，但 `internal/cli/tuiapp/model_stream.go`、`internal/cli/tuiapp/model_view.go`、`internal/cli/tuiapp/model_activity.go` 仍然很重。

### 2.3 当前热点文件

按 `wc -l` 的当前结果，主要热点如下：

- `cmd/cli/console.go`: 2668
- `internal/acp/server.go`: 2502
- `internal/cli/tuiapp/model_view.go`: 1544
- `internal/cli/tuiapp/model_stream.go`: 1439
- `kernel/runtime/runner.go`: 1267
- `kernel/runtime/task_manager.go`: 980
- `cmd/cli/acp.go`: 845
- `cmd/cli/main.go`: 751

这说明当前重构重点应该是“按职责拆分已有热点”，而不是先发明新层次。

## 3. 本次重构目标

本轮重构只追求以下目标：

1. 让 kernel 与适配层的依赖方向更清晰。
2. 把 runtime / task / ACP / TUI 的热点文件按现有职责边界拆开。
3. 把 durable state / transient state 规则写清楚、测清楚，避免后续继续堆隐式内存状态。
4. 收敛 CLI 与 ACP 启动装配的重复逻辑。
5. 在不破坏现有协议和存储兼容性的前提下提升可维护性。

## 4. 非目标

本轮不做以下事情：

- 不重写 agent loop。
- 不改 `session.Event` / `session.Store` 的基础契约。
- 不修改 ACP JSON-RPC 方法名和 payload 结构。
- 不把 `internal/acp` 整体搬进 `kernel`。
- 不在第一轮引入全新的“通用远程 Agent 协议层”。
- 不为了“目录好看”做大范围 rename/move。

如果执行过程中必须触碰这些点，需要拆出单独阶段并先补设计说明。

## 5. 必须保持的不变量

### 5.1 Session/Event 恢复语义

- `session.Event` 是运行历史的基础单位。
- `session.Store` 中的事件和状态快照必须继续支持会话恢复。
- `ContextWindowStore`、`CursorStore`、`StateUpdateStore` 这类扩展接口必须继续兼容。

### 5.2 Durable / transient 规则

以当前实现为准：

- `session.IsUIOnly(ev)` 不持久化。
- `session.IsOverlay(ev)` 不持久化。
- lifecycle event 不持久化。
- partial event 在 `PersistPartialEvents=false` 时不持久化。
- 即使 partial event 被持久化，durable replay 仍会过滤 partial event。

这些规则分别体现在 `kernel/runtime/runtime.go` 的 `shouldPersistEvent` 和 `kernel/runtime/runner.go` 的 `isDurableReplayEvent` 中。重构时必须保持行为等价，除非有单独设计变更。

### 5.3 恢复补偿语义

- `buildRecoveryEvents` 需要继续为 dangling tool call 生成补偿事件。
- `ReconcileSession` 需要继续在 run 启动前修复 bash/delegate task 状态。
- 任务恢复依赖 `task.Store` 与 runtime lifecycle，不能被 UI 层逻辑替代。

### 5.4 公开契约稳定

以下接口/契约在本轮默认视为稳定：

- `runtime.RunRequest`
- `Runtime.Run`
- `task.Manager`
- `tool.Tool`
- `policy.Hook`
- `plugin.Registry`
- `docs/kernel_contract_v1.md` 中定义的 event/error contract
- ACP 的 RPC 方法名与对外 payload

### 5.5 测试与兼容

- 每个阶段结束后，相关包测试必须通过。
- 任何涉及持久化格式、event meta、task result shape 的改动，都必须补兼容测试。

## 6. 当前主要问题

### 6.1 `kernel/runtime/runner.go` 责任过宽

当前一个文件里混合了：

- run lease
- replay buffer
- durable replay / resync
- submission 应用
- overlay run
- loop detect
- compaction 触发
- lifecycle 产出
- event append / stream emit

这不是抽象缺失问题，而是代码组织过于集中。

### 6.2 `kernel/runtime/task_manager.go` 责任聚合过多

当前同一文件里混合了：

- task record 持久化桥接
- controller rebuild
- bash task 控制
- delegate task 控制
- turn-level cleanup
- task list / wait / cancel / write

它不是 UI 文件，但仍然是典型的 orchestration hotspot。

### 6.3 CLI 与 ACP 的装配逻辑分散

`cmd/cli/main.go` 和 `cmd/cli/acp.go` 都在处理：

- execution runtime 初始化
- plugin registry 注册
- bootstrap assemble
- model factory / config 组装
- MCP manager 接入

这会让启动路径改动变成多点同步。

### 6.4 `internal/acp/server.go` 过于集中

当前同一文件里混合了：

- RPC dispatch
- auth
- session lifecycle
- prompt input/attachment 处理
- partial chunking
- tool call / task update 投影
- ACP session config 持久化
- tool result 清洗

这里最需要的是“按职责拆文件”，不是先做 kernel 化。

### 6.5 TUI 渲染逻辑仍然偏重

`internal/cli/tuiapp/model_stream.go` 和 `model_view.go` 已经显著偏大，且包含不少可以纯函数化的投影与渲染逻辑，导致测试成本较高。

## 7. 重构原则

1. 先保护行为，再优化结构。
2. 先同包拆分，再跨包迁移。
3. 先抽纯函数和 helper，再抽状态对象。
4. kernel 只承载运行时契约与编排核心；CLI/ACP/TUI 的展示和交互细节不能反向污染 kernel。
5. `sessionstream` 保持 raw session event 语义；`taskstream` 保持 task UI update 语义；不要强行合并。
6. 若一个新抽象当前只有一个调用点，优先拆文件，不急着拆包。

## 8. 执行顺序

必须按以下顺序推进，不要跳步。

### Phase 0: 建立护栏

目标：先把现有恢复/持久化/协议语义用测试固定住。

工作项：

- 检查并补齐 runtime 的 durable/transient 规则测试。
- 检查并补齐 task recovery 与 reconcile 测试。
- 检查并补齐 ACP 对 tool result 清洗、session config round-trip、partial update 投影的测试。
- 如发现 `docs/kernel_scope.md` 或 `docs/kernel_contract_v1.md` 与实际代码不一致，先修文档，不做结构改动。

完成标准：

- 不引入新的抽象层。
- 所有新增内容以测试或文档校准为主。

### Phase 1: 在 `kernel/runtime` 内部拆分 `runner.go`

目标：只做同包拆分，不改公开 API。

建议拆分方向：

- replay / resync
- submission / overlay
- lifecycle emit
- persistence policy
- loop detect / step tracking

要求：

- `Runtime.Run`、`Runner`、`RunRequest` 保持不变。
- `shouldPersistEvent`、`isDurableReplayEvent`、`streamResyncEvent` 这类逻辑要与 replay/persist 语义放在一起。
- 不在本阶段修改 compaction 规则或 event contract。

完成标准：

- `kernel/runtime` 测试通过。
- 对外行为无变化。

### Phase 2: 在 `kernel/runtime` 内部拆分 task orchestration

目标：把 `task_manager.go` 的职责按控制器和持久化桥接拆开。

建议拆分方向：

- manager shell
- task record persistence / rebuild
- bash controller
- delegate controller
- shared snapshot / state helper

要求：

- 保持 `newTaskManager` 入口稳定。
- 保持 `task.Manager` 与 `task.Store` 契约稳定。
- 保持 `taskstream` result metadata shape 不变。
- 不要把 task 控制逻辑搬到 CLI/ACP。

完成标准：

- `kernel/runtime`、`kernel/task`、`kernel/tool` 相关测试通过。

### Phase 3: 抽取共享的应用装配层

目标：收敛 `cmd/cli/main.go` 与 `cmd/cli/acp.go` 的重复启动逻辑。

允许新增一个共享内部包，位置建议：

- `internal/app/...`
- 或等价的单一内部装配包

这个共享层只负责：

- execution runtime 初始化
- plugin registry 注册
- bootstrap assemble
- model factory 构建
- MCP manager 初始化
- session/task store 组装

这个共享层不负责：

- CLI flag 解析
- ACP RPC 处理
- TUI 渲染

完成标准：

- `cmd/cli/main.go`、`cmd/cli/acp.go` 明显瘦身。
- CLI 与 ACP 的现有行为、flag、配置兼容性不变。

### Phase 4: 拆分 `internal/acp/server.go`

目标：按职责拆文件，不改 ACP 协议。

建议拆分方向：

- RPC dispatch / request routing
- auth / initialize
- session create/load/config state
- prompt input normalization / attachment loading
- event/tool/task update projection
- tool result sanitize / helper

要求：

- `Server`、`ServerConfig`、RPC 方法名与 payload shape 保持兼容。
- 先抽纯 helper，再拆 request handler。
- 本阶段不把 ACP 抽象提升到 kernel。

完成标准：

- `internal/acp` 测试通过。
- `cmd/cli/acp.go` 无协议层回归。

### Phase 5: 拆分 TUI 热点文件

目标：把可纯函数化的投影/渲染逻辑从大文件中拆出。

建议拆分方向：

- log chunk normalization
- assistant/reasoning block merge
- mutation summary merge
- tool output fade / activity block helper
- view projection helper

要求：

- `tuiapp.Model` 的外部行为不变。
- 不要在 TUI 阶段修改 kernel event contract。
- 如果出现 CLI transcript 与 TUI 展示分歧，优先保持上游原始事件不变，在适配层解决。

完成标准：

- `internal/cli/tuiapp`、`cmd/cli` 相关测试通过。

### Phase 6: 单独评估的后续议题

以下议题不属于本轮必做项，只能在前五个阶段完成后单独立项：

- ACP 作为 kernel 级 remote peer / remote delegation 协议
- 统一远程 capability 模型
- 更强的 import boundary / complexity lint 强制
- 更激进的包级重组

如果要做，必须新增单独设计文档，而不是顺手在本轮里夹带。

## 9. 每个阶段的执行规则

每个阶段都必须满足以下规则：

1. 先做最小结构拆分，再做命名清理。
2. 不在同一个阶段里同时做“跨包迁移 + 行为调整”。
3. 触碰持久化语义时，先补测试，再改代码。
4. 触碰 ACP 对外协议时，立即停止本阶段，拆出单独设计。
5. 如果某个拆分会制造 import cycle，优先退回“同包多文件”方案。
6. 每个阶段结束后至少跑该阶段相关包测试；最终阶段结束后跑一次 `go test ./...`。

## 10. 建议的验收清单

每个阶段提交前检查：

- 编译通过。
- 相关测试通过。
- 对外接口未无意变化。
- event/task result/session state 的 shape 未无意变化。
- 文档引用的文件路径和包名仍然存在。
- 没有新增新的 god file。

## 11. 明确禁止的做法

- 不要把“未来可能需要的抽象”一次性全部引入。
- 不要在没有测试护栏时重写 `runner.go` 或 `task_manager.go`。
- 不要把 ACP 的服务端实现改造成半成品的 kernel 协议层。
- 不要把 UI 展示规则写回 `kernel/runtime`。
- 不要修改持久化数据 shape 却不补兼容测试和迁移说明。

## 12. Agent 落地方式

Agent 必须按以下工作流执行：

1. 先完成一个阶段的代码与测试。
2. 阶段完成后给出“本阶段改了什么、没改什么、验证结果”。
3. 只有当前阶段通过，才进入下一阶段。

如果需要一句话概括本 Spec：

**先用测试锁住恢复与协议语义，再沿着当前代码里已经存在的职责缝隙，把 runtime、task、CLI/ACP 组装、ACP server、TUI 逐步拆薄；第一轮不做理想化的大重构。**
