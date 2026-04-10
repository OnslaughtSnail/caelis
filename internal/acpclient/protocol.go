package acpclient

import (
	"encoding/json"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
)

const (
	JSONRPCVersion = internalacp.JSONRPCVersion

	MethodInitialize           = internalacp.MethodInitialize
	MethodAuthenticate         = internalacp.MethodAuthenticate
	MethodSessionNew           = internalacp.MethodSessionNew
	MethodSessionList          = internalacp.MethodSessionList
	MethodSessionLoad          = internalacp.MethodSessionLoad
	MethodSessionSetMode       = internalacp.MethodSessionSetMode
	MethodSessionSetConfig     = internalacp.MethodSessionSetConfig
	MethodSessionPrompt        = internalacp.MethodSessionPrompt
	MethodSessionCancel        = internalacp.MethodSessionCancel
	MethodSessionUpdate        = internalacp.MethodSessionUpdate
	MethodSessionReqPermission = internalacp.MethodSessionReqPermission
	MethodReadTextFile         = internalacp.MethodReadTextFile
	MethodWriteTextFile        = internalacp.MethodWriteTextFile
	MethodTerminalCreate       = internalacp.MethodTerminalCreate
	MethodTerminalOutput       = internalacp.MethodTerminalOutput
	MethodTerminalWaitForExit  = internalacp.MethodTerminalWaitForExit
	MethodTerminalKill         = internalacp.MethodTerminalKill
	MethodTerminalRelease      = internalacp.MethodTerminalRelease
)

const (
	UpdateUserMessage   = internalacp.UpdateUserMessage
	UpdateAgentMessage  = internalacp.UpdateAgentMessage
	UpdateAgentThought  = internalacp.UpdateAgentThought
	UpdateToolCall      = internalacp.UpdateToolCall
	UpdateToolCallState = internalacp.UpdateToolCallState
	UpdateAvailableCmds = internalacp.UpdateAvailableCmds
	UpdatePlan          = internalacp.UpdatePlan
	UpdateCurrentMode   = internalacp.UpdateCurrentMode
	UpdateConfigOption  = internalacp.UpdateConfigOption
	UpdateSessionInfo   = internalacp.UpdateSessionInfo
)

type Message = internalacp.Message
type RPCError = internalacp.RPCError
type Implementation = internalacp.Implementation
type FileSystemCapabilities = internalacp.FileSystemCapabilities
type ClientCapabilities = internalacp.ClientCapabilities
type AuthMethod = internalacp.AuthMethod
type PromptCapabilities = internalacp.PromptCapabilities
type SessionListCapability = internalacp.SessionListCapability
type SessionCapabilities = internalacp.SessionCapabilities
type MCPCapabilities = internalacp.MCPCapabilities
type AgentCapabilities = internalacp.AgentCapabilities
type ProtocolVersion = internalacp.ProtocolVersion
type InitializeRequest = internalacp.InitializeRequest
type InitializeResponse = internalacp.InitializeResponse
type AuthenticateRequest = internalacp.AuthenticateRequest
type AuthenticateResponse = internalacp.AuthenticateResponse
type MCPServer = internalacp.MCPServer
type NewSessionRequest = internalacp.NewSessionRequest
type NewSessionResponse = internalacp.NewSessionResponse
type LoadSessionRequest = internalacp.LoadSessionRequest
type LoadSessionResponse = internalacp.LoadSessionResponse
type SetSessionModeRequest = internalacp.SetSessionModeRequest
type SetSessionModeResponse = internalacp.SetSessionModeResponse
type SetSessionConfigOptionRequest = internalacp.SetSessionConfigOptionRequest
type SetSessionConfigOptionResponse = internalacp.SetSessionConfigOptionResponse
type SessionListRequest = internalacp.SessionListRequest
type SessionSummary = internalacp.SessionSummary
type SessionListResponse = internalacp.SessionListResponse
type TextContent = internalacp.TextContent
type ImageContent = internalacp.ImageContent
type PromptRequest = internalacp.PromptRequest
type PromptResponse = internalacp.PromptResponse
type SessionMode = internalacp.SessionMode
type SessionModeState = internalacp.SessionModeState
type SessionConfigSelectOption = internalacp.SessionConfigSelectOption
type SessionConfigOption = internalacp.SessionConfigOption
type CancelRequest = internalacp.CancelNotification
type ToolCallLocation = internalacp.ToolCallLocation
type ToolCallContent = internalacp.ToolCallContent
type ToolCall = internalacp.ToolCall
type ToolCallUpdate = internalacp.ToolCallUpdate
type PlanEntry = internalacp.PlanEntry
type PlanUpdate = internalacp.PlanUpdate
type CurrentModeUpdate = internalacp.CurrentModeUpdate
type SessionInfoUpdate = internalacp.SessionInfoUpdate
type PermissionOption = internalacp.PermissionOption
type RequestPermissionRequest = internalacp.RequestPermissionRequest
type RequestPermissionResponse = internalacp.RequestPermissionResponse
type EnvVariable = internalacp.EnvVariable
type CreateTerminalRequest = internalacp.CreateTerminalRequest
type CreateTerminalResponse = internalacp.CreateTerminalResponse
type TerminalOutputRequest = internalacp.TerminalOutputRequest
type TerminalExitStatus = internalacp.TerminalExitStatus
type TerminalOutputResponse = internalacp.TerminalOutputResponse
type WaitForTerminalExitRequest = internalacp.WaitForTerminalExitRequest
type WaitForTerminalExitResponse = internalacp.WaitForTerminalExitResponse
type KillTerminalRequest = internalacp.KillTerminalRequest
type ReleaseTerminalRequest = internalacp.ReleaseTerminalRequest
type ReadTextFileRequest = internalacp.ReadTextFileRequest
type ReadTextFileResponse = internalacp.ReadTextFileResponse
type WriteTextFileRequest = internalacp.WriteTextFileRequest
type WriteTextFileResponse = internalacp.WriteTextFileResponse

type CancelResponse struct{}

// SessionNotification stays client-local so the raw update payload remains a
// json.RawMessage for delayed, type-specific decoding.
type SessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// ContentChunk stays client-local for the same reason: callers want access to
// the raw content payload before choosing a concrete content shape.
type ContentChunk struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
}

type TextChunk struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Keep the client-side shape permissive for forward compatibility. The server
// side uses typed command/config entries, but the client only needs a generic
// decoded payload today.
type AvailableCommandsUpdate struct {
	SessionUpdate     string           `json:"sessionUpdate"`
	AvailableCommands []map[string]any `json:"availableCommands"`
}

type ConfigOptionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	ConfigOptions any    `json:"configOptions"`
}
