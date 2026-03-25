package acpclient

import "encoding/json"

const (
	JSONRPCVersion = "2.0"

	MethodInitialize           = "initialize"
	MethodAuthenticate         = "authenticate"
	MethodSessionNew           = "session/new"
	MethodSessionList          = "session/list"
	MethodSessionLoad          = "session/load"
	MethodSessionPrompt        = "session/prompt"
	MethodSessionCancel        = "session/cancel"
	MethodSessionUpdate        = "session/update"
	MethodSessionReqPermission = "session/request_permission"
	MethodReadTextFile         = "fs/read_text_file"
	MethodWriteTextFile        = "fs/write_text_file"
	MethodTerminalCreate       = "terminal/create"
	MethodTerminalOutput       = "terminal/output"
	MethodTerminalWaitForExit  = "terminal/wait_for_exit"
	MethodTerminalKill         = "terminal/kill"
	MethodTerminalRelease      = "terminal/release"
)

const (
	UpdateUserMessage   = "user_message_chunk"
	UpdateAgentMessage  = "agent_message_chunk"
	UpdateAgentThought  = "agent_thought_chunk"
	UpdateToolCall      = "tool_call"
	UpdateToolCallState = "tool_call_update"
	UpdateAvailableCmds = "available_commands_update"
	UpdatePlan          = "plan"
	UpdateCurrentMode   = "current_mode_update"
	UpdateConfigOption  = "config_option_update"
	UpdateSessionInfo   = "session_info_update"
)

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type Implementation struct {
	Name    string `json:"name,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type ClientCapabilities struct {
	FS       FileSystemCapabilities `json:"fs"`
	Terminal bool                   `json:"terminal"`
}

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PromptCapabilities struct {
	Audio           bool `json:"audio"`
	EmbeddedContext bool `json:"embeddedContext"`
	Image           bool `json:"image"`
}

type SessionListCapability struct{}

type SessionCapabilities struct {
	List *SessionListCapability `json:"list,omitempty"`
}

type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

type AgentCapabilities struct {
	LoadSession     bool                `json:"loadSession"`
	MCPCapabilities MCPCapabilities     `json:"mcpCapabilities"`
	Prompt          PromptCapabilities  `json:"promptCapabilities"`
	Session         SessionCapabilities `json:"sessionCapabilities"`
}

type ProtocolVersion uint16

type InitializeRequest struct {
	ProtocolVersion    ProtocolVersion    `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

type InitializeResponse struct {
	ProtocolVersion   ProtocolVersion   `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod      `json:"authMethods,omitempty"`
}

type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}

type AuthenticateResponse struct{}

type MCPServer map[string]any

type NewSessionRequest struct {
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type LoadSessionRequest struct {
	SessionID  string      `json:"sessionId"`
	CWD        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

type LoadSessionResponse struct{}

type SessionListRequest struct {
	Cursor string `json:"cursor,omitempty"`
	CWD    string `json:"cwd,omitempty"`
}

type SessionSummary struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type SessionListResponse struct {
	Sessions   []SessionSummary `json:"sessions"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type PromptRequest struct {
	SessionID string            `json:"sessionId"`
	Prompt    []json.RawMessage `json:"prompt"`
	Meta      map[string]any    `json:"_meta,omitempty"`
}

type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

type CancelRequest struct {
	SessionID string `json:"sessionId"`
}

type CancelResponse struct{}

type SessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type ContentChunk struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
}

type TextChunk struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ToolCall struct {
	SessionUpdate string             `json:"sessionUpdate"`
	ToolCallID    string             `json:"toolCallId"`
	Title         string             `json:"title"`
	Kind          string             `json:"kind,omitempty"`
	Status        string             `json:"status,omitempty"`
	RawInput      any                `json:"rawInput,omitempty"`
	RawOutput     any                `json:"rawOutput,omitempty"`
	Content       []ToolCallContent  `json:"content,omitempty"`
	Locations     []ToolCallLocation `json:"locations,omitempty"`
}

type ToolCallUpdate struct {
	SessionUpdate string             `json:"sessionUpdate"`
	ToolCallID    string             `json:"toolCallId"`
	Title         *string            `json:"title,omitempty"`
	Kind          *string            `json:"kind,omitempty"`
	Status        *string            `json:"status,omitempty"`
	RawInput      any                `json:"rawInput,omitempty"`
	RawOutput     any                `json:"rawOutput,omitempty"`
	Content       []ToolCallContent  `json:"content,omitempty"`
	Locations     []ToolCallLocation `json:"locations,omitempty"`
}

type ToolCallLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

type ToolCallContent struct {
	Type       string `json:"type"`
	Content    any    `json:"content,omitempty"`
	TerminalID string `json:"terminalId,omitempty"`
}

type PlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []PlanEntry `json:"entries"`
}

type AvailableCommandsUpdate struct {
	SessionUpdate     string           `json:"sessionUpdate"`
	AvailableCommands []map[string]any `json:"availableCommands"`
}

type CurrentModeUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	CurrentModeID string `json:"currentModeId"`
}

type ConfigOptionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	ConfigOptions any    `json:"configOptions"`
}

type SessionInfoUpdate struct {
	SessionUpdate string  `json:"sessionUpdate"`
	Title         *string `json:"title,omitempty"`
	UpdatedAt     *string `json:"updatedAt,omitempty"`
}

type RequestPermissionRequest struct {
	SessionID string         `json:"sessionId"`
	ToolCall  ToolCallUpdate `json:"toolCall"`
	Options   []struct {
		OptionID string `json:"optionId"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

type RequestPermissionResponse struct {
	Outcome json.RawMessage `json:"outcome"`
}

type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CreateTerminalRequest struct {
	SessionID       string        `json:"sessionId"`
	Command         string        `json:"command"`
	Args            []string      `json:"args,omitempty"`
	CWD             string        `json:"cwd,omitempty"`
	Env             []EnvVariable `json:"env,omitempty"`
	OutputByteLimit *int          `json:"outputByteLimit,omitempty"`
}

type CreateTerminalResponse struct {
	TerminalID string `json:"terminalId"`
}

type TerminalOutputRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type TerminalExitStatus struct {
	ExitCode *int   `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

type TerminalOutputResponse struct {
	Output     string              `json:"output"`
	Truncated  bool                `json:"truncated"`
	ExitStatus *TerminalExitStatus `json:"exitStatus,omitempty"`
}

type WaitForTerminalExitRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type WaitForTerminalExitResponse struct {
	ExitCode *int   `json:"exitCode,omitempty"`
	Signal   string `json:"signal,omitempty"`
}

type KillTerminalRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type ReleaseTerminalRequest struct {
	SessionID  string `json:"sessionId"`
	TerminalID string `json:"terminalId"`
}

type ReadTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Line      *int   `json:"line,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
}

type ReadTextFileResponse struct {
	Content string `json:"content"`
}

type WriteTextFileRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type WriteTextFileResponse struct{}
