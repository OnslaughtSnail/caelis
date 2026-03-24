# TUI 流式渲染丢字问题排查与修复记录

## 背景

在 TUI 主回答区的流式输出阶段，偶发出现以下现象：

- 个别中文字符不可见，滚动窗口或触发重新渲染后又恢复
- 后续又观察到 emoji 或 emoji 后的第一个中文字符不可见
- 问题主要发生在 markdown 列表、段落开头、行首 emoji 等位置
- 非流式阶段基本不出现
- 最终文本内容本身是正确的，缺失发生在渲染过程，不是数据丢失

这个问题跨多个终端可复现，因此不能简单归因于某个终端模拟器。

## 结论

根因不在 markdown 解析、颜色样式或最终文本内容本身，而在**流式输出时的增量重绘粒度过细**。

具体来说：

1. 我们将上游 delta 拆成 grapheme cluster 后，按很小的 budget 逐帧吐给 Bubble。
2. 某些帧会恰好停在“宽字符 cluster 边界”上，例如：
   - 单个汉字
   - 单个 emoji
   - markdown 列表前缀后的 emoji
   - emoji 后紧跟的第一个汉字
3. 这些过碎的中间态会导致终端增量擦除/重绘时出现 cell 级漏画。
4. 一旦触发完整 repaint，文字又会恢复，因为底层文本状态其实没有错。

因此，这个问题本质上是：

**流式平滑播放策略把同一行切得过碎，暴露了宽字符和 emoji 在增量重绘中的脆弱边界。**

## 已排除方向

排查过程中，以下方向已经做过实验，结论是否定的：

- 去掉 assistant/reasoning 的 markdown 转换逻辑，问题仍在
- 去掉着色逻辑，问题仍在
- 去掉 narrative 专用 wrap，问题仍在
- 去掉 viewport 后的 scrollbar / indent / TrimRight，问题仍在

这些实验说明问题不在最终内容组织，而在 streaming 阶段“如何一帧一帧喂给渲染器”。

## 最终修复策略

修复思路不是识别 CJK 或 emoji 做特判，而是**提升流式帧的稳定性**。

### 1. 主回答流统一走 RawDelta 平滑播放

将主回答流统一到 `RawDeltaMsg` 路径，避免 live partial、final、reasoning、answer 走不同代码路径，减少行为分叉。

相关位置：

- `cmd/cli/console.go`
- `internal/cli/tuiapp/app.go`

### 2. 降低每帧刷新压力

通过帧合并和较低 FPS，让 streaming 更像稳定文本片段，而不是字符级刷屏。

当前默认参数：

- `StreamTickInterval = 33ms`
- `tea.WithFPS(30)`

相关位置：

- `cmd/cli/console_tui_tea.go`

### 3. 引入“最小稳定批量”

核心修复在 `chooseRevealClusterCount`。

思路：

- 不允许只吐出过小的宽字符片段
- 即使命中自然边界，也必须先满足最小稳定批量
- 当前规则：
  - 至少 `2` 个 grapheme cluster
  - 至少 `6` 个显示列

这样可以显著减少：

- 单个汉字单帧出现
- 单个 emoji 单帧出现
- `- emoji` 这种列表前缀被单独渲染

相关位置：

- `internal/cli/tuiapp/stream_blocks.go`

### 4. 默认增大每帧最大 reveal 上限

如果 `maxPerFrame` 太小，稳定批量规则即使存在也会被硬上限卡住。

最终调整为：

- normal: `5`
- catch-up: `12`

相关位置：

- `internal/cli/tuiapp/state.go`

### 5. 单帧尽量不跨当前逻辑行

后续优化中，又把 reveal 限制在“当前逻辑行”内优先完成。

这相当于给每一行独立的流式 budget：

- 当前行还没稳定展示完时，不急着跨到下一行
- markdown 列表里每一项的行首 emoji 因此更少被拆成脆弱中间态

相关位置：

- `internal/cli/tuiapp/stream_blocks.go`
  - `firstLogicalLineClusterLimit`
  - `chooseRevealClusterCount`

## 为什么这个方案有效

这个方案有效，不是因为它“修复了终端”，而是因为它**减少了会触发渲染问题的中间帧数量**。

换句话说：

- 以前：一行会经历很多极碎的中间态
- 现在：一行更倾向于按稳定的小片段出现

只要不频繁把渲染停在单个宽字符、单个 emoji、或列表前缀这种边界上，漏画概率就会明显下降。

## 附带修复：图片 attachment 用户消息重复

排查过程中还发现一个独立问题：

- 本地提交用户消息时，attachment token 的显示格式
- 与 runtime 回放 `UserMessageMsg` 时的显示格式

两者不一致，导致同一条消息可能被视为不同文本，从而重复展示两行。

修复方式：

- 统一 attachment token 的 display 拼接规则
- 使用统一的空格策略，避免 `[image: ...]` 与前后文本粘连

相关位置：

- `internal/cli/tuiapp/attachment_state.go`

## 后续排查原则

如果未来再出现类似问题，建议按下面顺序判断：

1. 先确认最终文本是否正确
2. 再确认问题是否只发生在 streaming 阶段
3. 优先检查 reveal 粒度，而不是先怀疑 markdown/wrap/theme
4. 避免新增“按字符类型特判”的分支
5. 优先从“更稳定的帧边界”入手修复

## 不建议的修法

以下方案不优先：

- 只对 CJK 特判
- 只对 emoji 特判
- 每次 streaming tick 强制全屏清空重绘
- 回退成完全不平滑的大块跳字

这些方案要么不优雅，要么副作用太大，要么后续维护成本高。

## 经验总结

这类问题最容易误判成：

- markdown 解析 bug
- wrap 算法 bug
- Bubble Tea 本身 bug
- 某个终端的兼容性问题

但这次的真正经验是：

**只要文本内容是对的，而重绘后能恢复，优先怀疑流式中间态是否切得太碎。**

对于 TUI 来说，“更细粒度”不一定意味着“更流畅”。  
在宽字符、emoji、ANSI 样式和增量重绘叠加时，**更稳定的帧边界**往往比**更高频的小步播放**更重要。
