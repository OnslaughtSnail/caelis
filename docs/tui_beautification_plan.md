# Plan: TUI 美化与用户体验产品化升级

从当前 MVP 级别的 Bubble Tea TUI 提升到现代化、产品化的终端交互体验。升级围绕视觉层次、交互流畅性、信息密度和专业化打磨四个维度展开，充分利用 Bubble Tea V2 + Lipgloss V2 + Bubbles V2 的新能力（declarative View、OnMouse、AltScreen 控制、BackgroundColorMsg 自适应、ProgressBar、Cursor 定制、KeyboardEnhancements 等）。

## Scope
- In: 视觉美化、交互流程优化、组件升级、动画/过渡效果、信息架构改进、主题系统增强
- Out: 核心 agent loop 逻辑改动、新 channel（GUI/Telegram）接入、ACP 协议层变更

---

## Phase 1: 视觉基础层升级

### 1.1 主题系统全面升级
[ ] 利用 `tea.BackgroundColorMsg` + `IsDark()` 实现启动时精确自适应明暗主题，替代当前环境变量猜测
[ ] 建立完整的语义化配色系统（Primary / Secondary / Accent / Success / Warning / Error / Muted / Surface），每个语义色在 light/dark 两套主题中有对应值
[ ] 引入 `lipgloss.LightDark(isDark)` 简化所有双主题色值定义，消除当前 if-else 分支
[ ] 增加 Adaptive 配色降级：利用 `tea.ColorProfileMsg` 检测终端 profile（TrueColor → 256 → ANSI），确保低端终端也有合理显示
- 涉及文件：`internal/cli/tuikit/theme.go`, `internal/cli/tuiapp/app.go`

### 1.2 排版与间距规范化
[ ] 统一所有 Block 的 vertical spacing：Block 之间统一 1 行间距，同类 Block 之间 0 行
[ ] 建立 content width 规范：主文本区最大 100 列居中，避免超宽终端下阅读困难
[ ] 规范左侧 gutter 宽度和样式：user/assistant/system 消息用不同 gutter 标识符（如 `›` / `●` / `▸`）
[ ] 改进分割线样式：从 `───` 升级为带渐变或题目的语义化分隔（如 `── Turn 3 ──`）
- 涉及文件：`internal/cli/tuiapp/blocks.go`, `internal/cli/tuiapp/view_layout.go`, `internal/cli/tuiapp/model_view.go`

### 1.3 字体与文本样式精细化
[ ] 为不同内容层级建立 Typography 系统：H1 Bold+大号色 / H2 Bold / Body / Caption / Code
[ ] Assistant 回答文本使用更高对比度的前景色 + 可选 italic reasoning 区分
[ ] 改进 inline code 样式：加 padding（` code `）+ 低对比度背景
[ ] 改进代码块样式：顶部显示语言标签、带行号选项、圆角边框
- 涉及文件：`cmd/cli/markdown_render.go`, `internal/cli/tuikit/theme.go`

---

## Phase 2: 核心交互组件升级

### 2.1 Composer 输入区升级
[ ] 引入 focus ring 视觉效果：输入框聚焦时显示高亮边框/底线，失焦时变为 muted
[ ] 改进 placeholder 样式：使用渐变/动态 placeholder 提示当前可用操作
[ ] 实现输入框高度自适应（1→N 行）：参考 bubbletea `dynamic-textarea` example，输入内容增长时平滑扩展到最大 8 行
[ ] 增加输入计数器：右下角显示字符数 / token 估算
[ ] 改进 attachment 显示：独立 chip 化展示（`📎 image.png ✕` / `📁 src/main.go ✕`），带删除交互
[ ] 改进 ghost hint（自动补全）样式：使用 dimmed italic 预览文本
- 涉及文件：`internal/cli/tuiapp/composer_view.go`, `internal/cli/tuiapp/composer_state.go`

### 2.2 Viewport 升级
[ ] 改进滚动条样式：使用细 track（`│`）+ 高亮 thumb（`█`），位于最右列
[ ] 实现平滑滚动动画：利用 `tea.Tick` 实现逐行/半页滚动过渡（参考 harmonica 弹簧动画库）
[ ] 增加 scroll-to-bottom 浮动按钮：当用户滚动离开底部时显示 `↓ New content` 指示
[ ] 改进 viewport 边界：到达顶/底时显示渐变 fade 效果（用 dimmed 文字模拟）
- 涉及文件：`internal/cli/tuiapp/model_view.go`, `internal/cli/tuiapp/view_layout.go`

### 2.3 命令面板 (Command Palette) 升级
[ ] 重新设计 slash command 面板为居中浮层样式（类 VS Code Command Palette）
[ ] 增加 fuzzy search 高亮：匹配字符加粗/着色显示
[ ] 为每个命令增加快捷键标签和描述说明
[ ] 增加命令分组：常用 / 模型 / 会话 / 工具 / 自定义
[ ] 实现命令 preview：选中命令时在右侧显示简要描述
- 涉及文件：`internal/cli/tuiapp/palette_overlay.go`（或新建）

---

## Phase 3: 状态展示与反馈系统

### 3.1 状态栏重构
[ ] 重新设计 header 状态栏为专业化布局：`[session icon] Session Name │ Model: claude-4-opus │ Workspace: caelis │ Tokens: 12.3k/200k`
[ ] 增加动态 breadcrumb：当在子 agent/task 中时显示层级路径
[ ] header 使用 lipgloss 反色/背景色渲染为视觉上的 solid bar
[ ] 重新设计 footer 状态栏：左侧显示快捷键提示（contextual），右侧显示 runtime 状态
- 涉及文件：`internal/cli/tuiapp/model_view.go`, `internal/cli/tuiapp/status_bar.go`（新建）

### 3.2 进度与加载指示
[ ] 升级 spinner 样式：使用 `spinner.MiniDot` + 主题色，替代当前默认 spinner
[ ] 增加任务级进度条：利用 `tea.ProgressBar`（V2 新增的 terminal progress bar）展示长任务进度
[ ] Tool 执行时显示 inline 进度：`🔧 Reading file... ━━━━━━━━━━── 3/10 files`
[ ] 改进 "thinking" 指示：reasoning 阶段使用脉冲动画 + 时间计数 `Thinking (3.2s)`
[ ] 增加 streaming 速度指示：在 assistant 输出时底部显示 tokens/sec
- 涉及文件：`internal/cli/tuiapp/blocks.go`, `internal/cli/tuiapp/stream_blocks.go`

### 3.3 通知与 Toast 系统
[ ] 实现轻量 toast 通知：右上角显示临时消息（文件已保存 / 操作已复制到剪贴板 / 错误提示）
[ ] toast 自动消失（3s）+ 手动关闭
[ ] 不同类型使用不同颜色：success(green) / warning(yellow) / error(red) / info(blue)
- 涉及文件：`internal/cli/tuiapp/toast.go`（新建）

---

## Phase 4: 内容区 Block 升级

### 4.1 Diff Block 升级
[ ] 改进 diff 渲染：使用红绿背景着色（不只是 +/- 前缀），类似 GitHub 风格
[ ] 增加 diff 统计摘要：`+42 -18` 带变化条柱 `████████░░`
[ ] 改进折叠/展开动画：从瞬间切换改为逐行 reveal
[ ] 增加 diff 文件路径美化：图标 + 相对路径 + 变更类型标签（Created / Modified / Deleted）
- 涉及文件：`internal/cli/tuiapp/blocks.go`（DiffBlock）

### 4.2 Bash Panel 升级
[ ] 改进 bash panel 头部：显示命令 + 耗时 + 退出码着色（0=green, non-zero=red）
[ ] 增加输出行数限制和 "Show more" 折叠
[ ] 改进 bash panel 边框：使用 lipgloss.RoundedBorder() 圆角
[ ] 增加命令复制按钮（鼠标可点击区域）
- 涉及文件：`internal/cli/tuiapp/blocks.go`（BashPanelBlock）

### 4.3 Activity/Tool Block 升级
[ ] 改进 tool call 展示：图标化分类（📁 文件操作 / 🔍 搜索 / 🌐 网络 / 💻 执行）
[ ] 展开态显示详细参数和结果，折叠态显示单行摘要
[ ] 增加 tool 执行时间追踪
[ ] 改进 subagent panel：增加子 agent 身份/头像、嵌套缩进、独立配色
- 涉及文件：`internal/cli/tuiapp/blocks.go`（ActivityBlock, SubagentPanelBlock）

### 4.4 Welcome 卡片升级
[ ] 重新设计 Welcome 卡片：ASCII art logo + 版本号 + 快速操作入口
[ ] 增加 "What's New" 摘要（版本更新时显示）
[ ] 增加 Quick Start tips：随机显示使用技巧
- 涉及文件：`internal/cli/tuiapp/blocks.go`（WelcomeBlock）

---

## Phase 5: 高级交互体验

### 5.1 键盘增强
[ ] 利用 `tea.KeyboardEnhancementsMsg` 检测 Kitty keyboard protocol 支持
[ ] 在支持的终端中启用 `ReportEventTypes` 获取按键释放事件
[ ] 增加更多快捷键：`Ctrl+K` 命令面板、`Ctrl+L` 清屏、`Ctrl+/` 帮助
[ ] 实现 vim-like 浏览模式：用 `j/k` 导航 blocks、`Enter` 展开/折叠
[ ] 改进快捷键提示：参考 bubbletea `help` example，底部显示 contextual key bindings
- 涉及文件：`internal/cli/tuiapp/keymap.go`, `internal/cli/tuiapp/model_input.go`

### 5.2 鼠标交互升级
[ ] 启用 `MouseModeCellMotion`：支持鼠标点击和拖拽滚动
[ ] 利用 V2 `View.OnMouse` 回调实现 block 级别鼠标交互：点击 diff 展开/折叠、点击文件路径复制
[ ] 改进面板拖拽：bash/subagent panel 支持鼠标拖拽调整高度
[ ] 增加 hover 效果：鼠标悬停在可交互元素上时变化光标/样式
- 涉及文件：`internal/cli/tuiapp/model_input.go`, `internal/cli/tuiapp/blocks.go`

### 5.3 Overlay/Modal 系统升级
[ ] 实现 z-order overlay 管理器：支持多层 modal 堆叠、ESC 逐层关闭
[ ] prompt modal 视觉升级：居中卡片 + 阴影效果（用边框模拟）+ 按钮高亮
[ ] 增加确认对话框组件：Yes/No 选择 + 快捷键 (Y/N)
[ ] 改进 approval prompt：更清晰的操作描述 + 彩色 diff preview 内嵌
- 涉及文件：`internal/cli/tuiapp/overlay.go`（新建或重构现有）

### 5.4 过渡动画
[ ] 实现 block 入场动画：新 block 从 dimmed 渐变到正常亮度（利用 Tick + 渐变色）
[ ] panel 展开/折叠使用逐行 reveal（逐帧增长/缩小行数）
[ ] overlay 出现/消失使用 fade 效果
[ ] 长操作完成时增加 brief flash 高亮
- 涉及文件：`internal/cli/tuiapp/animation.go`（新建）

---

## Phase 6: 信息架构与体验完善

### 6.1 会话管理视图
[ ] 新增 session picker overlay：列表展示历史会话 + 模糊搜索 + 预览
[ ] 每个会话显示：标题/摘要、时间、token 使用量、模型
[ ] 支持 session 删除 / 重命名操作
- 涉及文件：`internal/cli/tuiapp/session_picker.go`（新建）

### 6.2 帮助系统
[ ] 实现 `?` / `F1` 帮助 overlay：全屏快捷键速查卡
[ ] 分类展示：导航 / 编辑 / 会话 / 高级
[ ] 支持搜索快捷键
- 涉及文件：`internal/cli/tuiapp/help_overlay.go`（新建）

### 6.3 可访问性
[ ] 确保所有颜色对比度符合 WCAG AA（4.5:1 正文、3:1 大字）
[ ] 增加 `--no-color` / `NO_COLOR` 环境变量支持
[ ] 所有 icon/emoji 有纯文本 fallback（检测终端是否支持 unicode）
[ ] 验证在 screen 和 tmux 中的降级体验
- 涉及文件：跨组件

### 6.4 性能与诊断
[ ] 利用 `tea.WithFPS()` 优化：idle 时降到 15 FPS，streaming 时恢复 30 FPS
[ ] 改进慢帧诊断：增加帧时间直方图到 debug view
[ ] 优化 View() 渲染：缓存 static block 渲染结果，只重新渲染 dirty blocks
[ ] 利用 lipgloss width caching 减少重复测量
- 涉及文件：`internal/cli/tuiapp/model_view.go`, `internal/cli/tuiapp/view_render.go`

---

## 实施优先级建议

| 优先级 | Phase | 预计影响 |
|--------|-------|---------|
| P0 | 1.1 主题自适应 + 1.2 排版规范 | 立即改善视觉一致性 |
| P0 | 2.1 Composer 升级 | 核心输入体验 |
| P0 | 3.1 状态栏重构 | 信息层次感 |
| P1 | 1.3 Typography + 4.1 Diff 升级 | 内容可读性 |
| P1 | 3.2 进度指示 + 4.2 Bash Panel | 操作反馈 |
| P1 | 5.1 键盘增强 | 效率用户体验 |
| P2 | 2.2 Viewport + 2.3 Command Palette | 导航体验 |
| P2 | 5.2 鼠标交互 + 5.3 Overlay 系统 | 交互丰富度 |
| P2 | 4.3 Tool Block + 4.4 Welcome | 信息展示 |
| P3 | 3.3 Toast + 5.4 动画 | 打磨细节 |
| P3 | 6.x 信息架构与可访问性 | 长期价值 |

## 技术依赖

- `charm.land/bubbletea/v2` v2.0.2 ✅（已升级）
- `charm.land/bubbles/v2` v2.1.0 ✅（已升级）
- `charm.land/lipgloss/v2` v2.0.2 ✅（已升级）
- `github.com/charmbracelet/glamour` v0.9.1 ✅（已有）
- 可选新增：`github.com/charmbracelet/harmonica`（弹簧动画）
- 可选新增：`github.com/charmbracelet/huh`（表单/prompt 组件）

## Open Questions
- 是否需要支持用户自定义主题（配置文件 / 环境变量）？
- 鼠标交互是否默认启用，还是 opt-in（部分 SSH/tmux 用户可能不喜欢）？
- 动画是否需要 `--no-animation` fallback 以支持低性能终端？
