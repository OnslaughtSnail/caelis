package acp

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	JSONRPCVersion                         = "2.0"
	CurrentProtocolVersion ProtocolVersion = 1

	MethodInitialize           = "initialize"
	MethodAuthenticate         = "authenticate"
	MethodSessionNew           = "session/new"
	MethodSessionList          = "session/list"
	MethodSessionLoad          = "session/load"
	MethodSessionSetMode       = "session/set_mode"
	MethodSessionSetConfig     = "session/set_config_option"
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
	StopReasonEndTurn   = "end_turn"
	StopReasonCancelled = "cancelled"
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

	// Subagent lifecycle/streaming updates.
	UpdateSubagentStart    = "subagent_start"
	UpdateSubagentStream   = "subagent_stream"
	UpdateSubagentToolCall = "subagent_tool_call"
	UpdateSubagentPlan     = "subagent_plan"
	UpdateSubagentDone     = "subagent_done"
)

const (
	ToolStatusPending    = "pending"
	ToolStatusInProgress = "in_progress"
	ToolStatusCompleted  = "completed"
	ToolStatusFailed     = "failed"
)

const (
	ToolKindRead    = "read"
	ToolKindEdit    = "edit"
	ToolKindSearch  = "search"
	ToolKindExecute = "execute"
	ToolKindFetch   = "fetch"
	ToolKindOther   = "other"
)

const (
	PermAllowOnce    = "allow_once"
	PermAllowAlways  = "allow_always"
	PermRejectOnce   = "reject_once"
	PermRejectAlways = "reject_always"
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

type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type ClientCapabilities struct {
	FS       FileSystemCapabilities `json:"fs"`
	Terminal bool                   `json:"terminal"`
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

type AgentCapabilities struct {
	LoadSession bool                `json:"loadSession"`
	Prompt      PromptCapabilities  `json:"promptCapabilities"`
	Session     SessionCapabilities `json:"sessionCapabilities"`
}

type ProtocolVersion uint16

func (p *ProtocolVersion) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*p = 0
		return nil
	}
	var value uint16
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("unsupported protocolVersion %s", trimmed)
	}
	*p = ProtocolVersion(value)
	return nil
}

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

type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type NewSessionRequest struct {
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId,omitempty"`
}

type NewSessionResponse struct {
	SessionID     string                `json:"sessionId"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
}

type SessionListRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

type SessionSummary struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd,omitempty"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type SessionListResponse struct {
	Sessions   []SessionSummary `json:"sessions"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

type LoadSessionRequest struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

type LoadSessionResponse struct {
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ImageContent struct {
	Type     string `json:"type"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
	Name     string `json:"name,omitempty"`
	URI      string `json:"uri,omitempty"`
}

type ResourceLink struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	URI         string `json:"uri"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type EmbeddedResourceData struct {
	URI      string `json:"uri"`
	Name     string `json:"name,omitempty"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

type EmbeddedResource struct {
	Type     string               `json:"type"`
	Resource EmbeddedResourceData `json:"resource"`
}

type PromptRequest struct {
	SessionID string            `json:"sessionId"`
	Prompt    []json.RawMessage `json:"prompt"`
}

type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionModeState struct {
	AvailableModes []SessionMode `json:"availableModes"`
	CurrentModeID  string        `json:"currentModeId"`
}

type CurrentModeUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	CurrentModeID string `json:"currentModeId"`
}

type SessionConfigSelectOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SessionConfigOption struct {
	Type         string                      `json:"type"`
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Description  string                      `json:"description,omitempty"`
	Category     string                      `json:"category,omitempty"`
	CurrentValue string                      `json:"currentValue"`
	Options      []SessionConfigSelectOption `json:"options"`
}

type AvailableCommandInput struct {
	Hint string `json:"hint,omitempty"`
}

type AvailableCommand struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Input       AvailableCommandInput `json:"input,omitempty"`
}

type AvailableCommandsUpdate struct {
	SessionUpdate     string             `json:"sessionUpdate"`
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

type PlanEntry struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type PlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	Entries       []PlanEntry `json:"entries"`
}

type ConfigOptionUpdate struct {
	SessionUpdate string                `json:"sessionUpdate"`
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

type SetSessionModeRequest struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type SetSessionModeResponse struct{}

type SetSessionConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type SetSessionConfigOptionResponse struct {
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

type SessionNotification struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

type ContentChunk struct {
	SessionUpdate string `json:"sessionUpdate"`
	Content       any    `json:"content"`
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

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type SelectedPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId"`
}

type CancelledPermissionOutcome struct {
	Outcome string `json:"outcome"`
}

type RequestPermissionResponse struct {
	Outcome json.RawMessage `json:"outcome"`
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

// SubagentStartUpdate notifies the client that a new subagent session has been spawned.
type SubagentStartUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	Agent         string `json:"agent"`
	CallID        string `json:"callId,omitempty"`
}

// SubagentStreamUpdate carries streaming content (assistant answer or reasoning) from a subagent.
type SubagentStreamUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	Stream        string `json:"stream"` // "assistant" or "reasoning"
	Chunk         string `json:"chunk"`
}

// SubagentToolCallUpdate carries tool call activity from a subagent.
type SubagentToolCallUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	ToolName      string `json:"toolName"`
	CallID        string `json:"callId,omitempty"`
	Args          string `json:"args,omitempty"`
	Stream        string `json:"stream,omitempty"` // "stdout", "stderr"
	Chunk         string `json:"chunk,omitempty"`
	Final         bool   `json:"final,omitempty"`
}

// SubagentPlanUpdate carries a plan snapshot from a subagent.
type SubagentPlanUpdate struct {
	SessionUpdate string      `json:"sessionUpdate"`
	SpawnID       string      `json:"spawnId"`
	Entries       []PlanEntry `json:"entries"`
}

// SubagentDoneUpdate notifies the client that a subagent session has completed.
type SubagentDoneUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	SpawnID       string `json:"spawnId"`
	State         string `json:"state"` // "completed", "failed", "interrupted"
}
