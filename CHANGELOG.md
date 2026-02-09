# Changelog

## v0.0.1 - 2026-02-09

- Initial `caelis` kernel + CLI release candidate.
- Unified model provider layer (`openai`, `openai_compatible`, `gemini`, `anthropic`, `deepseek`).
- Built-in core tools (`READ`) and workspace/shell tools with execution runtime abstraction.
- MCP ToolSet integration via `~/.agents/mcp_servers.json` (`mcpServers` schema).
- Session persistence and workspace-isolated session index (SQLite).
- Context compaction with watermark strategy and manual `/compact`.
- Skills metadata discovery and prompt injection.
- Real-model eval runner with CI light gate and nightly suite.
- CLI interactive commands: model switching, connect, sessions, compaction, status, tool display.
