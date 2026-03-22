package acpclient

import "encoding/json"

const (
	JSONRPCVersion = "2.0"

	MethodInitialize           = "initialize"
	MethodAuthenticate         = "authenticate"
	MethodSessionNew           = "session/new"
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
	UpdateUserMessage      = "user_message_chunk"
	UpdateAgentMessage     = "agent_message_chunk"
	UpdateAgentThought     = "agent_thought_chunk"
	UpdateToolCall         = "tool_call"
	UpdateToolCallState    = "tool_call_update"
	UpdatePlan             = "plan"
	UpdateSubagentStart    = "subagent_start"
	UpdateSubagentStream   = "subagent_stream"
	UpdateSubagentToolCall = "subagent_tool_call"
	UpdateSubagentPlan     = "subagent_plan"
	UpdateSubagentDone     = "subagent_done"
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

type ProtocolVersion uint16

type InitializeRequest struct {
	ProtocolVersion    ProtocolVersion    `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

type InitializeResponse struct {
	ProtocolVersion ProtocolVersion `json:"protocolVersion"`
	AuthMethods     []AuthMethod    `json:"authMethods,omitempty"`
}

type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}

type AuthenticateResponse struct{}

type NewSessionRequest struct {
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId,omitempty"`
}

type NewSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type LoadSessionRequest struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

type LoadSessionResponse struct{}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type PromptRequest struct {
	SessionID string            `json:"sessionId"`
	Prompt    []json.RawMessage `json:"prompt"`
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
	SessionUpdate string `json:"sessionUpdate"`
	ToolCallID    string `json:"toolCallId"`
	Title         string `json:"title"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
	RawInput      any    `json:"rawInput,omitempty"`
	RawOutput     any    `json:"rawOutput,omitempty"`
}

type ToolCallUpdate struct {
	SessionUpdate string  `json:"sessionUpdate"`
	ToolCallID    string  `json:"toolCallId"`
	Title         *string `json:"title,omitempty"`
	Kind          *string `json:"kind,omitempty"`
	Status        *string `json:"status,omitempty"`
	RawInput      any     `json:"rawInput,omitempty"`
	RawOutput     any     `json:"rawOutput,omitempty"`
}

type PlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []PlanEntry `json:"entries"`
}

type SubagentStartUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	Agent         string `json:"agent"`
	CallID        string `json:"callId,omitempty"`
}

type SubagentStreamUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	Stream        string `json:"stream"`
	Chunk         string `json:"chunk"`
}

type SubagentToolCallUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	ToolName      string `json:"toolName"`
	CallID        string `json:"callId,omitempty"`
	Args          string `json:"args,omitempty"`
	Stream        string `json:"stream,omitempty"`
	Chunk         string `json:"chunk,omitempty"`
	Final         bool   `json:"final,omitempty"`
}

type SubagentPlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	SpawnID       string      `json:"spawnId"`
	Entries       []PlanEntry `json:"entries"`
}

type SubagentDoneUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	State         string `json:"state"`
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
	SessionID string        `json:"sessionId"`
	Command   string        `json:"command"`
	Args      []string      `json:"args,omitempty"`
	CWD       string        `json:"cwd,omitempty"`
	Env       []EnvVariable `json:"env,omitempty"`
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
