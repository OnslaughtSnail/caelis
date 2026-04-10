# ACP 交互架构：单轨主会话 + 控制器切换 + 结构化 Handoff

## 一、架构总览

Caelis 的交互架构围绕**单轨主会话**（single-track main session）组织。所有主控事件
——无论来自本地 kernel (self) 还是 ACP 外部控制器——都写入同一个 session JSONL。
ACP 外部参与者和 SPAWN 子代理保留独立的 projection log。

```
┌─────────────────────────────────────────────────────┐
│  Presentation (TUI)                                  │
│  stream_acp_projection.go → MainACPTurnBlock         │
│                            → ParticipantTurnBlock    │
│                            → SubagentPanel           │
├─────────────────────────────────────────────────────┤
│  Projection                                          │
│  acpprojector.LiveProjector → ACPProjectionMsg       │
│    appendNarrativeChunk (live 渲染，不持久化主控)     │
├─────────────────────────────────────────────────────┤
│  Persistence                                         │
│  session JSONL ← 主控事件 (self + ACP)               │
│  acp_projection.jsonl ← 仅 participant + subagent    │
├─────────────────────────────────────────────────────┤
│  Controller Runtime                                  │
│  self: kernel gw.RunTurn                             │
│  ACP main: persistentMainACPState + PromptParts      │
│  ACP participant: externalAgentTurn                  │
│  SPAWN subagent: kernel delegation                   │
└─────────────────────────────────────────────────────┘
```

## 二、核心设计原则

1. **单轨持久化**：主控 self 和 ACP 事件写入同一 session JSONL，不再维护主控的独立 projection log
2. **ACP 是外部黑盒协议**：不通过 kernel runtime / agent.Agent 语义解释 ACP 输出
3. **chunk 到达就 append**：live 路径不做 narrative trim / baseline strip / replay suffix merge
4. **Controller Epoch 模型**：每次控制器切换生成新 epoch，通过 epoch_id 标注事件归属
5. **Handoff 而非 Replay**：控制器切换时构建结构化 Handoff Packet，不依赖远端 LoadSession replay
6. **LoadSession 仅用于重连**：连续多轮直接调用 PromptParts，不每轮重建远端会话

## 三、控制器 Epoch 模型

### ControllerEpoch

每次主控切换（self→ACP 或 ACP→self 或 ACP 换 agent）产生一个新的 epoch：

```go
type ControllerEpoch struct {
    EpochID        string // 唯一标识，格式 "ep-<nanoid>"
    ControllerKind string // "self" 或 "acp"
    ControllerID   string // ACP agent ID 或空
}
```

- 持久化在 session state 中，key: `acp.controllerEpoch`
- 每个主控事件的 Meta 中携带 `controller_kind`、`controller_id`、`epoch_id`

### RemoteSyncState

追踪与某个远端 ACP controller 的同步状态，防止重复注入 handoff：

```go
type RemoteSyncState struct {
    ControllerID       string // 对应的 ACP agent ID
    RemoteSessionID    string // 上一次使用的远端 session ID
    LastHandoffEventID string // 上一次 handoff 覆盖到的最后一个本地事件 ID
    LastHandoffEpochID string // 上一次 handoff 时的 epoch ID
    HandoffHash        string // 预留：handoff 内容摘要
}
```

- 持久化在 session state 中，key: `acp.remoteSync`
- `LastHandoffEventID` 作为水位线：增量 handoff 只发送该 ID 之后的新事件

## 四、结构化 Handoff

### HandoffPacket

控制器切换时（self→ACP / ACP 换 agent / 重连）构建的结构化上下文数据包：

```go
type HandoffPacket struct {
    Objective              string
    DurableConstraints     string
    CurrentStatus          string
    Decisions              string
    WorkspaceFacts         string
    ArtifactsOrFileChanges []string
    RecentUserRequests     []string
    OpenTasks              string
    RecentTranscriptTail   string
    SourceEventRange       [2]string // [first_event_id, last_event_id]
    Mode                   string    // "full" 或 "incremental"
}
```

### Handoff 触发条件

| 场景 | Mode | 说明 |
|------|------|------|
| 首次使用 ACP | full | 完整 checkpoint + 尾部 transcript |
| 同一 agent 换新 session | full | 新 session 需要完整上下文 |
| 重连同一 session | incremental | 仅发送 LastHandoffEventID 之后的新事件 |
| 切换 ACP agent | full | 新 agent 需要完整上下文 |

### Handoff 流程

1. `buildHandoffPacket(events, syncState)` 根据 sync state 决定 full/incremental
2. 从 session events 中提取 checkpoint、transcript tail、file changes、user requests
3. `RenderHandoffText()` 将 packet 渲染为 Markdown 文本
4. 注入到 ACP `PromptParts` 的 userTurnParts
5. `updateRemoteSyncState()` 更新水位线，防止下次重复注入

## 五、ACP 主控持久化客户端

### persistentMainACPState

ACP 客户端在 turn 之间保持存活，避免每轮重建子进程和 session：

```go
type persistentMainACPState struct {
    client          mainACPClient
    agentID         string
    remoteSessionID string
    epochID         string
    capabilities    AgentCapabilities
    closed          bool
    onUpdate        func(UpdateEnvelope)  // 可变的每轮回调
}
```

- `OnUpdate` 使用可变分发模式：`Config.OnUpdate` → `state.dispatchUpdate()` → `state.onUpdate`
- 每轮通过 `state.setOnUpdate(fn)` 设置当前轮的回调
- 当控制器切换或 agent 改变时调用 `closePersistentMainACP()` 关闭

### 生命周期

```
首次 ACP 轮 ──── ensurePersistentMainACPClient ──── acpclient.Start
                           │
                    advanceControllerEpoch
                           │
                    ensureMainACPRemoteSession ────── NewSession (首次)
                           │
                    buildHandoffPacket (full)
                           │
                    client.PromptParts ────────────── OnUpdate → dispatchUpdate
                           │
连续 ACP 轮 ──── reuse client + remoteSessionID ──── PromptParts 直接调用
                           │
重连 ────────── ensurePersistentMainACPClient ──── acpclient.Start (新进程)
                           │
                    ensureMainACPRemoteSession ────── LoadSession (恢复远端 session)
                           │
                    buildHandoffPacket (incremental)
                           │
agent 切换 ──── closePersistentMainACP ────────── client.Close
                           │
                    ensurePersistentMainACPClient ──── 新 agent / 新 client
```

### LoadSession 语义

| 场景 | 是否调用 LoadSession |
|------|---------------------|
| 同一客户端连续多轮 | ❌ 直接 PromptParts |
| 新客户端进程 + 存储了远端 session ID | ✅ LoadSession |
| 新客户端进程 + 无远端 session 记录 | ❌ NewSession |
| app 重启后恢复 | ✅ LoadSession |

## 六、三条 ACP 路径

### 路径 1：ACP 主控 (mainAgent=ACP)

- **入口**: `runPreparedACPMainSubmissionContext` (acp_main_turn.go)
- **控制器**: `persistentMainACPState` → `client.PromptParts`
- **投影**: `mainACPProjectionTracker` 包装 `LiveProjector`
  - `Project(env)` → 实时流式投影，直接转发 LiveProjector 输出
  - 不做 Finalize / adjustProjection / 叙述重写
- **事件录制**: `mainACPTurnRecorder` 累积 canonical events，写入 session JSONL
  - 每个事件 Meta 携带 `controller_kind=acp`、`controller_id`、`epoch_id`
- **TUI 渲染**: `emitMainACPProjectionMsg` → 发送 `ACPProjectionMsg` 到 TUI（**不写 projection log**）

### 路径 2：外部参与者 (/agent slash commands)

- **入口**: `handleExternalAgentPrompt` (external_agent_slash.go)
- **控制器**: `externalAgentTurn` → `acpclient.PromptParts`
- **投影**: 独立的 `LiveProjector` 实例
- **持久化**: `appendExternalParticipantProjection` → `ACPProjectionStore.AppendParticipantProjection`
- **TUI**: `ACPProjectionMsg{Scope: ACPProjectionParticipant}` → `ParticipantTurnBlock`

### 路径 3：SPAWN 子代理

- **入口**: kernel delegation 通过 `tool.SpawnToolName`
- **投影**: `projectSubagentUpdate` (spawn_preview.go)
- **持久化**: `sendSubagentProjectionMsg` → `ACPProjectionStore.PersistSubagentProjectionMsg`
- **TUI**: `ACPProjectionMsg{Scope: ACPProjectionSubagent}` → `SubagentPanel`

## 七、self↔ACP 切换机制

### 切换触发

- 用户执行 `/agent use <name>` 或 `/agent use self`
- 通过 `switchMainAgent` → `configStore.SetMainAgent` 持久化选择
- 仅在空闲状态才允许切换
- 切换时调用 `closePersistentMainACP()` 关闭现有 ACP 客户端

### self → ACP 切换

1. 下一次 `preparePromptSubmission` 解析 `usesACP=true`
2. `advanceControllerEpoch(ControllerKindACP, agentID)` 产生新 epoch
3. `ensurePersistentMainACPClient` 启动 ACP 客户端（如不存在）
4. `ensureMainACPRemoteSession` 创建新远端 session
5. `buildHandoffPacket(events, syncState)` 构建完整 handoff
6. `client.PromptParts` 携带 handoff 内容发起 turn

### ACP → self 切换

1. `closePersistentMainACP()` 关闭 ACP 客户端
2. 下一次 `preparePromptSubmission` 解析 `usesACP=false`
3. `runPreparedSubmissionContext` 运行本地 kernel
4. 之前 ACP turn 中 `mainACPTurnRecorder` 录制的 canonical events 已在 session JSONL 中
5. 本地 kernel 继续在相同 session 上执行，自然看到 ACP 历史

## 八、Resume 策略

### 单轨 Replay

Resume/load session 时，`renderSessionEvents` 直接从 session JSONL 重建 TUI 状态：

1. 遍历 session events
2. 对于带 `_ui_agent` meta 的事件 → `replayMainACPSessionEvent` 生成 ACP TUI 消息
3. 对于普通事件 → 正常的 session event 渲染
4. Participant 和 subagent 仍从各自的 projection log replay

```
Session JSONL events
    │
    ├── event.Meta["_ui_agent"] != "" ──── replayMainACPSessionEvent
    │                                        │
    │                                        ├── user → ACPProjectionMsg{DeltaText}
    │                                        ├── assistant → ACPProjectionMsg{DeltaText}
    │                                        ├── tool_call → ACPProjectionMsg{ToolCallID}
    │                                        └── tool_response → ACPProjectionMsg{ToolStatus}
    │
    └── normal event ──── forwardEventToTUIWithOptionsContext
```

### 关键不变量

- Resume 不调用任何远端 ACP API（没有 batch LoadSession）
- Resume 不从 projection log 读取主控 ACP 历史
- Resume 仅依赖本地 session JSONL

## 九、事件元数据标注

每个 ACP 主控事件在 Meta 中携带：

| Key | 说明 | 示例 |
|-----|------|------|
| `_ui_agent` | ACP agent 标识（用于 TUI 路由） | `"copilot"` |
| `controller_kind` | 控制器类型 | `"acp"` |
| `controller_id` | ACP agent ID | `"copilot"` |
| `epoch_id` | 当前 epoch 标识 | `"ep-bK3xN..."` |

## 十、Epoch Handoff Layer（独立交接层）

### 架构定位

Epoch Handoff Layer 是一个**独立的编排层**，位于 kernel (self) 和 ACP controller 之上。
它不属于 kernel 也不属于 ACP 协议层，而是作为独立抽象负责：

- epoch 边界识别
- checkpoint 生成（`summarize_epoch`）
- handoff bundle 组装
- full / incremental 交接规则
- remote sync waterline 管理

```
┌─────────────────────────────────────────────────────┐
│  Presentation (TUI / Desktop)                        │
├─────────────────────────────────────────────────────┤
│  Epoch Handoff Layer  (internal/epochhandoff)        │
│  HandoffCoordinator                                  │
│    ├── BuildCheckpoint (summarize_epoch)              │
│    ├── BuildHandoffBundle                             │
│    ├── ComputeIncrementalRange                        │
│    ├── RenderLLMView                                  │
│    ├── PersistCheckpoint                              │
│    └── UpdateSyncWaterline                            │
├─────────────────────────────────────────────────────┤
│  Controller Runtime                                  │
│  ├── self: kernel gw.RunTurn                         │
│  └── ACP: persistentMainACPState + PromptParts       │
└─────────────────────────────────────────────────────┘
```

### EpochCheckpoint

每个 controller epoch 最多产生一个 canonical checkpoint。Checkpoint 明确分为两层：

#### SystemFields（系统字段，不暴露给 LLM）

| 字段 | 用途 |
|------|------|
| `checkpoint_id` | 唯一标识 |
| `epoch_id` | 所属 epoch |
| `controller_kind` | "self" 或 "acp" |
| `controller_id` | ACP agent ID |
| `source_event_start/end` | 事件范围 |
| `created_at` | 创建时间 |
| `created_by` | "rule" 或 "summarize_turn" |
| `mode` | "full" 或 "incremental" |
| `watermark_event_id` | 水位线 |
| `schema_version` | schema 版本 |
| `hash` | LLM 内容摘要 |

#### LLMFields（LLM 字段，注入给下一个 controller）

| 字段 | 用途 |
|------|------|
| `objective` | 当前目标 |
| `durable_constraints` | 持久约束 |
| `current_status` | 当前状态 |
| `completed_work` | 已完成工作 |
| `artifacts_changed` | 变更的文件 |
| `important_results` | 重要结果 |
| `decisions` | 已做决策 |
| `open_tasks` | 待办任务 |
| `risks_or_unknowns` | 风险/未知 |
| `recent_user_requests` | 近期用户请求 |
| `handoff_notes` | 交接备注 |

**关键不变量**：
- `system_fields` 永远不会出现在 LLM 注入内容中
- `llm_fields` 只包含任务交接必要信息，不包含 internal ID、ACP 私有 tool 语义等

### summarize_epoch

`summarize_epoch` 是独立抽象，属于 Epoch Handoff Layer，不属于 kernel 或 ACP。

支持两种模式：

1. **Mode A: 规则/本地 builder 生成**（当前实现）
   - `epochhandoff.BuildCheckpoint()` 从 session 事件中规则提取
   - 能提取的不交给 LLM 自由发挥

2. **Mode B: 借助接棒主 Agent 执行临时 summarize_epoch turn**（预留）
   - 该 turn 不计入正式工作历史
   - 只持久化 checkpoint 结果
   - Schema 受 Epoch Handoff Layer 约束

### HandoffBundle

控制器切换时由 Epoch Handoff Layer 组装 `HandoffBundle`：

- 包含 relevant checkpoint 列表
- 可选的 recent transcript tail
- sync watermark

面向 LLM 的注入内容**只来自 `llm_fields`**。`system_fields` 只用于 bundle 组装和水位线判断。

### Synthetic User Message 注入

Handoff 制品不放入 system prompt，而是投影为 **synthetic user message**：

```text
[System-generated handoff checkpoint]

This is a structured handoff artifact generated by Caelis.
It summarizes prior execution intervals that you did not directly execute.
Treat it as trusted working context, not as a new user request.

## Active Objective
...
## Current Status
...
```

- 使用 `model.RoleUser` 保证协议兼容性
- 明确标识为系统生成，不是普通用户请求
- `system_fields` 不允许直接暴露给 LLM

### Full / Incremental 规则

| 场景 | Mode | 说明 |
|------|------|------|
| 首次使用 ACP | full | 完整 checkpoint + transcript tail |
| 同一 agent 换新 session | full | 新 session 需要完整上下文 |
| 重连同一 session | incremental | 仅发送水位线之后的新内容 |
| 切换 ACP agent | full | 新 agent 需要完整上下文 |
| 同一 remote session 复用 | 不发送 | 连续多轮直接 PromptParts |

水位线机制通过 `RemoteSyncState.LastHandoffEventID` 实现：
- 增量 handoff 只发送该 ID 之后的新事件对应的 checkpoint
- `self → ACP → self → ACP` 多轮切换不会重复注入完整 checkpoint

### ACP → self 语义安全

ACP 结果交接给 self 时：
- LLM fields 只保留中性的工作结果和任务状态
- 不暴露 ACP 私有 tool 名（如 `copilot_edit_file`）
- 文件变更用中性描述（如 `Modified: /src/main.go`）
- self 后续上下文不被 ACP 私有 capability 污染

## 十一、变更历史

### Phase 6：Epoch Handoff Layer（当前）

| 变更 | 文件 | 影响 |
|------|------|------|
| 新增 `internal/epochhandoff` 包 | internal/epochhandoff/*.go | Epoch Handoff Layer 独立抽象 |
| `EpochCheckpoint` (system/llm 分层 schema) | epochhandoff/checkpoint.go | 分层 checkpoint 模型 |
| `BuildCheckpoint` (summarize_epoch Mode A) | epochhandoff/summarizer.go | 规则提取 checkpoint |
| `HandoffBundle` + `RenderLLMView` | epochhandoff/bundle.go | bundle 组装 + LLM 视图渲染 |
| `HandoffCoordinator` | epochhandoff/coordinator.go | 协调器：checkpoint 持久化、bundle 构建、waterline |
| `SyntheticHandoffMessage` | epochhandoff/injection.go | synthetic user message 注入 |
| `ComputeIncrementalRange` | epochhandoff/coordinator.go | full/incremental 计算 |
| `buildHandoffBundle` bridge | cmd/cli/handoff.go | 旧 HandoffPacket → 新 HandoffBundle 桥接 |
| 重写 handoff 注入逻辑 | cmd/cli/acp_main_turn.go | 使用 HandoffBundle.RenderLLMView |
| 17 项回归测试 | epochhandoff/epochhandoff_test.go | 覆盖全部验收标准 |

### Phase 5：单轨化重构

| 变更 | 文件 | 影响 |
|------|------|------|
| 添加 `ControllerEpoch` / `RemoteSyncState` | pkg/acpmeta/epoch.go | 控制器 epoch 和远端同步状态模型 |
| 添加 `HandoffPacket` / `buildHandoffPacket` | cmd/cli/handoff.go | 结构化 handoff 构建 |
| 添加 `persistentMainACPState` | cmd/cli/acp_main_turn.go | ACP 客户端跨轮持久化 |
| 重写 `runPreparedACPMainSubmissionContext` | cmd/cli/acp_main_turn.go | 使用持久客户端 + handoff builder |
| 重写 `ensureMainACPRemoteSession` | cmd/cli/acp_main_turn.go | LoadSession 仅用于重连 |
| 移除 `mainACPFreshSessionSeed` | cmd/cli/acp_main_turn.go | 被 handoff builder 替代 |
| 移除 `mainACPCheckpointAndRecentHistory` | cmd/cli/acp_main_turn.go | 被 handoff builder 替代 |
| 事件标注 `controller_kind` / `epoch_id` | cmd/cli/acp_main_turn.go | annotateMainACPEvent 扩展 |
| 停止主控 projection log 写入 | cmd/cli/console.go | emitMainACPProjectionMsg / emitMainACPTurnStart |
| 单轨 replay `replayMainACPSessionEvent` | cmd/cli/console.go | renderSessionEvents 新增 |
| agent 切换时关闭持久客户端 | cmd/cli/agent_cmd.go | handleAgent + closePersistentMainACP |
| ACP→self 关闭持久客户端 | cmd/cli/console.go | runPreparedSubmissionContext |

### Phase 4：统一投影转换层

| 变更 | 文件 | 影响 |
|------|------|------|
| 提取 `projectionToACPMsg` | acp_projection_helpers.go | 统一 Projection→ACPProjectionMsg |
| 提取 `replayProjectionMsgFromEvent` | acp_projection_helpers.go | 统一 replay 转换 |
| 提取 `acpMsgToPersistedProjection` | acp_projection_helpers.go | 统一持久化转换 |

### Phase 3：ACP 事件路径隔离

| 变更 | 文件 | 影响 |
|------|------|------|
| LiveProjector 使用 `appendNarrativeChunk` | acpprojector/live.go | 简化前缀去重 |
| 移除 bridge 函数 | console.go | `forwardMainACPStreamEvent` / `forwardMainACPCanonicalEvent` |
| `mainACPProjectionState` 简化 | console.go | 仅保留 turnStarted |

## 十二、残留风险与后续建议

### Landed Invariants（已固化的不变量）

1. **单轨持久化**: 主控 ACP 事件仅写入 session JSONL，不写 projection log
2. **统一投影转换层**: 三条路径共享 `projectionToACPMsg` / `replayProjectionMsgFromEvent` / `acpMsgToPersistedProjection`
3. **ACP 路径隔离**: ACP 事件不经过 kernel session stream 渲染
4. **Controller Epoch**: 每次切换产生新 epoch，事件 Meta 携带 epoch_id
5. **LoadSession 语义**: 仅用于重连（新客户端 + 已有远端 session），不用于普通多轮
6. **Epoch Handoff Layer 独立**: `summarize_epoch` 逻辑不在 kernel 内部，位于独立的 `internal/epochhandoff` 包
7. **Checkpoint 分层 Schema**: `system_fields` 和 `llm_fields` 严格分离，system_fields 不进入 LLM 注入
8. **Synthetic User Message 注入**: handoff 不修改 system prompt，而是投影为带明确标识的 user message
9. **ACP → self 语义安全**: checkpoint llm_fields 不包含 ACP 私有 tool 名和 provider 语义

### 后续建议

1. **summarize_epoch Mode B**: 当前仅实现 Mode A (规则提取)，后续可实现 Mode B (借助 LLM 的临时 summarize turn)
2. **ACP→self epoch filter**: ACP 录制的 tool call/result 事件在 self kernel 上下文构建中需要 epoch filter 防止语义泄露
3. **Projection log 清理**: 主控路径不再写入 projection log，`AppendMainProjection` / `AppendMainTurnStart` / `ReplayMainEvents` 可标记 deprecated
4. **持久客户端健康检查**: `persistentMainACPState` 目前依赖 `isAlive()` 检查 agent ID 匹配，后续可增加心跳机制
5. **HandoffCoordinator 集成**: 逐步将 `cmd/cli` 中的旧 `buildHandoffPacket` 调用迁移到 `HandoffCoordinator` 方法
6. **Checkpoint compaction**: 长会话中 checkpoint 数量增多，需要 checkpoint 合并策略
2. **ACP 事件路径严格隔离**: ACP 事件仅通过 ACP client OnUpdate 回调渲染，kernel session stream 不参与
3. **Live 路径无后处理**: `appendNarrativeChunk` 是唯一的叙述去重机制，不使用启发式重叠
4. **Replay 从 projection log 恢复**: 不依赖 canonical assistant event 补 ACP 历史
5. **三条 ACP 路径 Scope 严格分离**: Main / Participant / Subagent 通过 `ACPProjectionScope` 隔离
6. **Persistence round-trip**: `acpMsgToPersistedProjection` → JSONL → `replayProjectionMsgFromEvent` 路径已有回归测试覆盖

### Transitional Areas（过渡区域）

### 已知局限

1. **BTW 路径未完全对齐**: `runBTWContext` 的 `preparePromptSubmission` 在 mainAgent=ACP 时可能返回 nil agent，但 BTW 仍尝试运行 self-kernel。需确认 BTW 在 ACP 模式下的行为语义。

2. **Projection log 回放依赖 JSONL 格式**: `acp_projection.jsonl` 没有版本化 schema，schema 演进需注意向后兼容。

### 建议的后续工作

- **BTW ACP 模式行为**：明确定义 BTW 在 mainAgent=ACP 时是否应路由到 ACP 或使用本地 fallback
- **统一 continuation seed API**：将 `mainACPFreshSessionSeed` 和 `renderSessionEvents` 的 checkpoint + transcript 逻辑提取为共享的 session context builder
- **Projection log compaction**：长会话的 projection log 可能增长过大，需要 log rotation 或 compaction 策略
