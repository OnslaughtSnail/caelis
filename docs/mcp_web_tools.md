# MCP Web Tools

`caelis` 当前不内建 `WEB_SEARCH` / `WEB_FETCH` provider。第一阶段推荐通过现有 `mcp_tools` 接入只读 Web 能力。

## 推荐做法

- 使用一个暴露 `search` / `fetch` 一类只读工具的 MCP server。
- 继续通过 `~/.agents/mcp_servers.json` 配置接入，不修改 kernel provider 边界。
- 把 Web 能力视为外部工具调用，默认保留授权确认。

## 示例配置

```json
{
  "cache_ttl_seconds": 30,
  "mcpServers": {
    "web": {
      "transport": "streamable",
      "url": "http://127.0.0.1:8787/mcp",
      "include_tools": ["search", "fetch"]
    }
  }
}
```

说明：

- `include_tools` 只保留只读 Web 工具，避免把浏览器自动化、登录态操作或写操作默认暴露给模型。
- 如果你的 MCP server 工具名不是 `search` / `fetch`，按服务端真实名称调整。
- `streamable`、`sse`、`stdio` 都可用，选型取决于 MCP server 提供的 transport。

## 授权与显示

- 外部 MCP 工具会走工具级授权，而不是自动放行。
- CLI/TUI 会尽量展示：
  - MCP tool 名称
  - 目标 URL 或 query 摘要
  - 请求参数摘要
- 这类结果仍按普通工具结果进入会话历史；工具 `metadata` 不会回灌给模型上下文。
