# ACP Reusable Layer Priority Plan

## Decision

在下面三项里，下一优先级最高的是：

- `ACP client/runtime` 继续拆成更独立的 reusable layer

不是：

- plugin / agent assembly
- app-owned mode / config

原因很简单：

1. `plugin / agent assembly` 需要一个稳定的 ACP client/agent runtime 边界，否则插件只能继续绑在当前 repo 的内部 wiring 上。
2. `mode / config` 需要建立在稳定的 session/client/server runtime adapter 之上，否则 `session/load/new/mode/config` 的 app-owned 语义会继续和产品 glue 混在一起。
3. 现在 repo 内已经有可运行的 ACP 基础设施，但还没有一个真正可以当作 “Go ACP SDK” 复用的独立层。

## Why This Is The Right Next Step

本地文档已经给出方向：

- [docs/kernel_acp_foundation_spec.md](/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/docs/kernel_acp_foundation_spec.md)
  明确要求 ACP foundation 覆盖 `schema/core/json-rpc/client runtime/server runtime/capability bridges`
- [docs/acp_infra_gap_analysis.md](/Users/xueyongzhi/WorkDir/xueyongzhi/caelis/docs/acp_infra_gap_analysis.md)
  明确把 reusable infrastructure 作为当前最大缺口

官方 ACP 生态现在已经有多语言 SDK，而 Go 仍是空位：

- ACP 官方 GitHub 组织列出了官方库：Kotlin、Python、Rust、TypeScript、Java  
  Source: [agentclientprotocol GitHub org](https://github.com/agentclientprotocol)
- Kotlin SDK 已明确拆出：
  - `acp-model`
  - `acp`
  - `acp-ktor`
  - `acp-ktor-client`
  - `acp-ktor-server`
  - `acp-ktor-test`  
  Source: [agentclientprotocol/kotlin-sdk](https://github.com/agentclientprotocol/kotlin-sdk)
- Python SDK 已明确强调：
  - generated schema models
  - stdio JSON-RPC plumbing
  - helper builders
  - session accumulators / tool trackers / permission brokers  
  Source: [agentclientprotocol/python-sdk](https://github.com/agentclientprotocol/python-sdk)

所以当前最合理的方向不是继续先做 plugin 或 mode/config，而是：

- 先把 `sdk/acp` 真正做成一个 **Go 版 reusable ACP SDK**
- 之后 plugin / agent assembly 和 app-owned mode/config 都基于这层开发

## Non-Goals

本轮不做：

- 插件市场或完整 plugin runtime
- app 级 mode/config 产品策略
- TUI/Gateway product glue
- 重新发明 ACP 协议或自定义 transport 语义

## Target Outcome

目标不是“当前 repo 能跑 ACP”，而是：

- 让 `sdk/acp` 变成一个可以被其他 Go 项目单独拿去用的 ACP SDK
- 让 `caelis` 本身成为这个 SDK 的一个 consumer，而不是把 ACP 基础设施和产品代码混在一起

## Reference Direction From Official SDKs

结合官方 SDK 的组织方式，Go 版目标结构建议收成：

```text
sdk/acp/
  schema/         // 纯协议模型和常量
  jsonrpc/        // 通用请求/响应/notification/conn
  transport/      // stdio 等 transport
  client/         // client runtime
  agent/          // agent/server runtime
  bridge/         // permission/fs/terminal 等 capability bridge
  testkit/        // conformance fixtures / fake transports / golden helpers
```

当前 repo 已经开始有雏形：

- `sdk/acp/schema`
- `sdk/acp/adapter.go`
- `sdk/acp/fixture`

但还没有完全收成上面这种正式边界。

## Recommended Public Boundaries

### 1. Schema Layer

职责：

- 协议 method 常量
- update 常量
- typed request/response/update structs
- terminal / permission payloads

要求：

- 纯数据层
- 不依赖 runtime/session/product code
- 作为唯一 schema 事实源

### 2. JSON-RPC / Transport Layer

职责：

- request/response dispatch
- pending call correlation
- notification delivery
- stdio transport

要求：

- 不理解 ACP session 语义
- 只做通用消息收发
- client 和 agent 共享

### 3. Client Runtime Layer

职责：

- initialize
- authenticate
- new/load session
- prompt / promptParts
- cancel
- terminal methods
- update decoding

要求：

- `core client` 与 `process spawning` 分离
- 允许未来接 stdio 之外的 transport
- 不掺入 `caelis` product policy

### 4. Agent Runtime Layer

职责：

- request dispatch
- callback plumbing
- prompt lifecycle
- session/update emission
- permission request round-trip

要求：

- runtime 只依赖明确 adapter boundary
- 不直接绑 `sdk/session` 或 `sdk/runtime/local`

### 5. Capability Bridges

职责：

- permission bridge
- fs bridge
- terminal bridge

要求：

- bridge 是 ACP runtime 的可插拔扩展
- `caelis` 只是其中一个 consumer

### 6. Testkit / Conformance

职责：

- fake callback recorder
- replay ordering fixtures
- cancellation fixtures
- update ordering assertions
- permission round-trip fixtures

要求：

- 其他包可复用
- 不依赖某个具体 LLM/provider

## What To Port From Official SDKs

建议 “借鉴结构和行为”，而不是机械照抄具体实现。

优先借鉴：

### Kotlin SDK

借鉴它的模块边界：

- model 独立
- runtime 与 transport 分层
- transport extras 独立
- test utilities 独立

### Python SDK

借鉴它的实用层：

- helper builders
- contrib accumulators
- permission brokers
- examples + tests 先行

## Concrete Next Phases

### Phase 1: Public Repackaging

目标：

- 把当前 `sdk/acp` 明确重组为：
  - `schema`
  - `jsonrpc`
  - `transport/stdio`
  - `client/core`
  - `agent/core`
  - `testkit`

输出：

- 不改现有行为
- 先改公开边界和目录组织
- 顶层 package 可以保留兼容 re-export

### Phase 2: Client/Agent Runtime Split

目标：

- 把当前 `sdk/acp/server.go` 里的 agent-side runtime 和 NDJSON stdio server 分开
- 把当前 process-spawn / local runtime 相关 glue 从 generic client 层进一步剥离

输出：

- one reusable client runtime
- one reusable agent runtime
- `caelis` adapter 只在外层组装

### Phase 3: Bridge Extraction

目标：

- permission bridge
- terminal bridge
- replay loader

统一收成 bridge 层

输出：

- `sdk/acp/bridge/permission`
- `sdk/acp/bridge/terminal`
- `sdk/acp/bridge/replay`

### Phase 4: Testkit and Conformance

目标：

- 补完整 ACP conformance-style fixtures
- 让 ordering/replay/update lifecycle/cancellation 不只存在于局部测试

输出：

- reusable testkit
- golden-style assertions
- CI-ready ACP conformance subset

## Why Plugin / Agent Assembly Should Wait

如果现在先做 plugin / agent assembly，会出现两个问题：

1. 插件要直接依赖当前 repo 的 ACP 实现细节
2. 未来 ACP SDK 一旦继续拆，插件装配边界还要再返工

更好的顺序是：

- 先把 ACP reusable layer 定稳
- 然后 plugin 只依赖稳定的：
  - ACP client factory
  - ACP agent descriptor
  - bridge adapters

## Why Mode / Config Should Wait

`mode/config` 的真正问题不是协议字段，而是 ownership：

- 哪些 state 是 app-owned
- 哪些是 session-owned
- mode/config 改动后怎样重装配 runtime

这些都更适合在 ACP reusable layer 定稳之后再做。

否则会把：

- session protocol
- app config semantics
- local runtime rebuild

重新混在一起。

## Immediate Recommended Work Items

下一轮建议按这个顺序做：

1. `sdk/acp/jsonrpc` + `sdk/acp/transport/stdio`
2. `sdk/acp/client/core` 与 process wrapper 分离
3. `sdk/acp/agent/core` 与 runtime adapter 分离
4. `sdk/acp/testkit` 正式化

然后再回头做：

5. plugin / agent assembly
6. app-owned mode/config

## Bottom Line

下一优先级最高的是：

- **把 `sdk/acp` 继续拆成一个真正可复用的 Go ACP SDK**

这是后续：

- plugin / agent assembly
- app-owned mode/config
- 更多外部 ACP agent 注入

共同依赖的底座。
