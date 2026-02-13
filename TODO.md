# Kernel TODO

目标：将 `kernel` 收敛为可复用 Agent 内核，具体产品行为放到上层应用与插件。

## 决策（已确认）

- [x] macOS 默认强制 `seatbelt`；不可用时不再宣称 sandbox 可选，降级为宿主机（白名单+审批）或 `full_control`
- [x] `session.Store` 等核心接口可允许 break（无需保守兼容）
- [x] 安全命令白名单、审批、sandbox/host 路由采用统一策略模型

## P0 内核契约与边界（当前优先）

- [x] 1. 移除 builtin provider 的隐式上下文注入
  - [x] `plugin/builtin.RegisterAll` 改为显式依赖注入（runtime/mcp manager）
  - [x] 删除 `plugin/builtin/context.go` 中 `context.WithValue` 传参
  - [x] 更新 `cmd/cli/main.go` 与 `kernel/bootstrap/bootstrap_test.go`

- [x] 2. 固化 kernel scope 文档
  - [x] 新增 `docs/kernel_scope.md`（kernel in/out of scope）
  - [x] 标注当前仍在 kernel 的产品化逻辑及迁移策略

- [x] 3. 统一错误契约（runtime + execenv）
  - [x] 合并并规范错误码命名空间与映射策略
  - [x] 补充 “错误码 -> 生命周期状态” 规则文档与测试

- [x] 4. 策略引擎升级为统一决策模型
  - [x] policy hook 支持 `allow/deny/require_approval` 决策对象
  - [x] 把 `safe-commands` + meta 字符判定并入统一策略
  - [x] BASH/execenv 只消费策略结果，不内嵌产品分支（sandbox command-not-found fallback 改为策略 metadata 驱动）

## P1 运行时核心能力增强（下一阶段）

- [ ] 5. 增加 RunGuard（按当前决策暂缓）
  - [ ] 限制单次 run 的 wall-clock、tool 调用数、输出上限
  - [ ] 超限时输出稳定错误码并可恢复控制权

- [x] 6. 上下文事件窗口优化（基于最近一次 compaction）
  - [x] 增加 `ContextWindowStore` 可选接口，默认返回“最近一次 compaction + 后续事件”
  - [x] runtime 构建上下文/压缩/usage 优先读取窗口事件，减少全量历史装载
  - [x] 保持 `Session`/`Event` 无状态设计，不引入额外状态字段

- [x] 7. compaction 策略可插拔
  - [x] 摘要提示词与语言从 runtime 内核剥离为 strategy
  - [x] runtime 只保留调度、阈值与事件落盘

- [ ] 8. provider health 接入主流程（按当前决策暂缓）
  - [ ] 启动后可选健康探测
  - [ ] 暴露统一状态查询入口给上层（CLI/API/Web）
